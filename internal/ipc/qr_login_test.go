package ipc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	userstore "myguo/internal/store"
)

func qrServiceFixture(t *testing.T, uid string, verify func(*http.Request)) (*HongguoService, *Store) {
	t.Helper()
	s, db := accountTestService(t, t.TempDir(), t.TempDir())
	if err := db.SetAuthenticatedAccount(userstore.AccountAuth{Cookies: "sessionid=original"}, UserProfile{UserID: "710000000000001", LoggedIn: true}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreSavedAccount(); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveHistory(HistoryItem{SeriesID: "710000000000101", Title: "existing", UpdatedAt: 100}); err != nil {
		t.Fatal(err)
	}
	var pngData bytes.Buffer
	png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 32, 32)))
	s.httpClient = &http.Client{Transport: ipcHandlerTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/passport/web/get_qrcode/":
			if r.Header.Get("Cookie") != "" {
				t.Error("current account leaked into QR attempt")
			}
			json.NewEncoder(w).Encode(map[string]any{"message": "success", "data": map[string]any{"token": "qr-secret", "qrcode": base64.StdEncoding.EncodeToString(pngData.Bytes()), "expire_time": 180}})
		case "/passport/web/check_qrconnect/":
			http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "new-session", Path: "/", Secure: true})
			w.Write([]byte(`{"message":"success","data":{"status":"3"}}`))
		case "/passport/account/info/v2/":
			w.Write([]byte(`{"message":"success","data":{"error_code":0}}`))
		case "/reading/user/info/v1/":
			if verify != nil {
				verify(r)
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"biz_user_id": uid, "user_name": "QR User"}})
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	})}}
	return s, db
}
func TestQRLoginConfirmsAndKeepsSameAccountHistory(t *testing.T) {
	s, db := qrServiceFixture(t, "710000000000001", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	code, err := s.BeginQRLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(code)
	if strings.Contains(string(raw), "qr-secret") {
		t.Fatal("server token exposed")
	}
	result, err := s.PollQRLogin(ctx, code.ID)
	if err != nil || result == nil || result.Profile == nil || !result.Profile.LoggedIn {
		t.Fatalf("login failed: %v", err)
	}
	rows, _ := db.GetHistory()
	if len(rows) != 1 {
		t.Fatal("same account QR cleared history")
	}
	auth, ok := db.GetAccountAuth()
	if !ok || !strings.Contains(auth.Cookies, "sessionid=new-session") {
		t.Fatal("confirmed session not persisted")
	}
}
func TestQRLoginChangesOwnerOnlyAfterConfirmation(t *testing.T) {
	s, db := qrServiceFixture(t, "710000000000099", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	code, err := s.BeginQRLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := db.GetHistory(); len(rows) != 1 {
		t.Fatal("opening QR cleared history")
	}
	if _, err = s.PollQRLogin(ctx, code.ID); err != nil {
		t.Fatal(err)
	}
	if rows, _ := db.GetHistory(); len(rows) != 0 {
		t.Fatal("new account inherited previous visible history")
	}
}
func TestCanceledQRVerificationCannotReplaceAccount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, db := qrServiceFixture(t, "710000000000099", func(*http.Request) { cancel() })
	code, err := s.BeginQRLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PollQRLogin(ctx, code.ID); err == nil {
		t.Fatal("canceled verification accepted")
	}
	if db.GetProfile().UserID != "710000000000001" {
		t.Fatal("canceled QR replaced account")
	}
	if rows, _ := db.GetHistory(); len(rows) != 1 {
		t.Fatal("canceled QR removed history")
	}
}
func TestReplacedQRAndLogoutCannotComplete(t *testing.T) {
	s, _ := qrServiceFixture(t, "710000000000099", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old, err := s.BeginQRLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.BeginQRLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PollQRLogin(ctx, old.ID); err == nil {
		t.Fatal("old attempt survived refresh")
	}
	if err = s.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PollQRLogin(ctx, next.ID); err == nil {
		t.Fatal("attempt survived logout")
	}
}

func TestQRLoginRetriesTemporaryAccountVerificationWithoutRescan(t *testing.T) {
	s, db := qrServiceFixture(t, "710000000000001", nil)
	original := s.httpClient.Transport
	failures := 0
	s.httpClient.Transport = detailAPITestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/reading/user/info/v1/" && failures == 0 {
			failures++
			return &http.Response{StatusCode: 502, Body: io.NopCloser(strings.NewReader("temporary")), Header: http.Header{}}, nil
		}
		return original.RoundTrip(r)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	code, err := s.BeginQRLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.PollQRLogin(ctx, code.ID)
	if err != nil || first.State != "confirming" || db.GetProfile().Nickname == "QR User" {
		t.Fatalf("temporary verification did not preserve attempt: %v %v", first, err)
	}
	result, err := s.PollQRLogin(ctx, code.ID)
	if err != nil || result.State != "confirmed" {
		t.Fatalf("retry required a new QR: %v %v", result, err)
	}
}
