package qrauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureQR(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func response(w http.ResponseWriter, data map[string]any) {
	json.NewEncoder(w).Encode(map[string]any{"message": "success", "data": data})
}
func newFixture(t *testing.T, handler http.HandlerFunc) *Session {
	t.Helper()
	qr := fixtureQR(t)
	tr := testTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		if r.URL.Path == "/passport/web/get_qrcode/" {
			if r.Header.Get("Cookie") != "" {
				t.Error("new QR reused an account cookie")
			}
			if r.URL.Query().Get("aid") != "513755" {
				t.Error("wrong web application")
			}
			http.SetCookie(w, &http.Cookie{Name: "passport_csrf_token", Value: "csrf", Path: "/", Secure: true})
			response(w, map[string]any{"token": "secret-qr-token", "qrcode": qr, "expire_time": 180, "error_code": 0})
		} else {
			handler(w, r)
		}
		return w.Result(), nil
	})
	s, err := New(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestPhoneConfirmationAndPassportAreBothRequired(t *testing.T) {
	calls := 0
	s := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-tt-passport-csrf-token") == "" && r.URL.Path != "/finish" {
			t.Error("missing CSRF cookie binding")
		}
		switch r.URL.Path {
		case "/passport/web/check_qrconnect/":
			calls++
			status := []string{"1", "2", "3"}[calls-1]
			response(w, map[string]any{"status": status, "redirect_url": "/finish"})
		case "/finish":
			http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "confirmed", Path: "/", Secure: true})
			w.WriteHeader(200)
		case "/passport/account/info/v2/":
			if !strings.Contains(r.Header.Get("Cookie"), "sessionid=confirmed") {
				t.Error("lost confirmed cookie")
			}
			response(w, map[string]any{"error_code": 0})
		default:
			t.Fatal("unexpected request")
		}
	})
	for _, want := range []string{"waiting", "scanned", "confirmed"} {
		state, credential, err := s.Poll(context.Background())
		if err != nil || state != want {
			t.Fatalf("poll=%q %v", state, err)
		}
		if want != "confirmed" && credential != nil {
			t.Fatal("credentials exposed before confirmation")
		}
		if want == "confirmed" && (credential == nil || !strings.Contains(credential.Cookies, "sessionid=confirmed")) {
			t.Fatal("missing confirmed credential")
		}
	}
}
func TestRejectedExpiredAndUnknownStateCannotLogin(t *testing.T) {
	for _, status := range []string{"4", "5", "unexpected"} {
		t.Run(status, func(t *testing.T) {
			s := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
				http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "not-proof", Path: "/", Secure: true})
				response(w, map[string]any{"status": status})
			})
			_, c, e := s.Poll(context.Background())
			if e == nil || c != nil {
				t.Fatal("nonconfirmed state accepted")
			}
		})
	}
}
func TestQRRedirectCannotSendCookiesToAnotherHost(t *testing.T) {
	s := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "hongguoduanju.com" {
			t.Fatal("cross-domain login request")
		}
		response(w, map[string]any{"status": "3", "redirect_url": "https://example.com/steal"})
	})
	_, c, err := s.Poll(context.Background())
	if err == nil || c != nil {
		t.Fatal("foreign redirect accepted")
	}
}
func TestPassportErrorDoesNotAuthenticateSessionCookie(t *testing.T) {
	s := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "check_qrconnect") {
			http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "unverified", Path: "/", Secure: true})
			response(w, map[string]any{"status": "3"})
		} else {
			json.NewEncoder(w).Encode(map[string]any{"message": "error", "data": map[string]any{"error_code": 8}})
		}
	})
	_, c, err := s.Poll(context.Background())
	if err == nil || c != nil {
		t.Fatal("Passport failure accepted")
	}
}
func TestQRErrorDoesNotExposeAuthorizationToken(t *testing.T) {
	s := newFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	s.client.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Get", URL: r.URL.String(), Err: errors.New("failure")}
	})
	_, _, err := s.Poll(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-qr-token") {
		t.Fatal("QR token leaked in error")
	}
}

func TestConfirmedSessionSurvivesTemporaryPassportFailureAndQRExpiry(t *testing.T) {
	checks, passport := 0, 0
	s := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "check_qrconnect") {
			checks++
			http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "confirmed", Path: "/", Secure: true})
			response(w, map[string]any{"status": "3"})
			return
		}
		passport++
		if passport <= 3 {
			w.WriteHeader(502)
			return
		}
		response(w, map[string]any{"error_code": 0})
	})
	if _, c, err := s.Poll(context.Background()); !errors.Is(err, ErrTemporary) || c != nil || !s.PhoneConfirmed() {
		t.Fatalf("confirmation was not retained: %v", err)
	}
	s.ExpiresAt = time.Now().Add(-time.Minute)
	state, c, err := s.Poll(context.Background())
	if err != nil || state != "confirmed" || c == nil || checks != 1 {
		t.Fatalf("confirmation could not recover: %s %v checks=%d", state, err, checks)
	}
}
