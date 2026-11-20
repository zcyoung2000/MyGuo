package auth

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters/memstore"
)

// Exercise the reference Full profile through the actual account transport,
// including the cookie/IID binding and exact transmitted-body digest.
func TestNativePlaybackUsesFullSignaturesThroughAccountTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != hgapi.PlaybackPath || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		argus, _ := base64.StdEncoding.DecodeString(r.Header.Get("x-argus"))
		ladon, _ := base64.StdEncoding.DecodeString(r.Header.Get("x-ladon"))
		sum := md5.Sum(body)
		if len(argus) != 4 || len(ladon) != 4 || len(r.Header.Get("x-medusa")) < 128 ||
			!strings.EqualFold(r.Header.Get("x-ss-stub"), hex.EncodeToString(sum[:])) {
			t.Error("playback request lacks the complete, body-bound signature envelope")
			return // Match the real upstream's empty HTTP 200 response.
		}
		if !strings.Contains(r.Header.Get("Cookie"), "sessionid=account") || !strings.Contains(r.Header.Get("Cookie"), "install_id="+r.URL.Query().Get("iid")) || r.Header.Get("x-tt-token") != "token-value" {
			t.Error("account transport lost credentials while forwarding the signed request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"7100000000000201":{"video_model":{"video_id":"fixture","video_duration":120,"video_list":[{"main_url":"https://media.invalid/720.mp4","video_meta":{"definition":"720p","codec_type":"h264"}}]}}}}`)
	}))
	defer server.Close()
	profiles := memstore.New()
	if err := profiles.Save(context.Background(), &hgapi.DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002", TTReq: "guest-ticket"}); err != nil {
		t.Fatal(err)
	}
	transport := New()
	transport.SetCredentials("sessionid=account", "token-value", "cn")
	client, err := hgapi.NewClient(hgapi.Options{Transport: transport, ProfileStore: profiles, APIOrigin: server.URL, RegisterOrigin: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	playback, err := client.ResolvePlayback(context.Background(), "7100000000000201", hgapi.PlaybackOptions{})
	if err != nil || playback == nil || len(playback.Streams) != 1 || playback.DurationSeconds != 120 {
		t.Fatalf("signed playback request failed: %v", err)
	}
}

func TestAuthTransportInjectsAccountCredentials(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = io.WriteString(w, `{"code":0,"data":[]}`)
	}))
	defer server.Close()

	transport := New()
	transport.SetCredentials("sessionid=account", "token-value", "cn-gd")
	response, err := transport.Do(context.Background(), &hgapi.Request{
		Method:  http.MethodGet,
		URL:     server.URL + "/reading/bookapi/bookmall/tab/v",
		Headers: map[string]string{"User-Agent": "agent"},
	})
	if err != nil {
		t.Fatalf("Do err = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got.Get("Cookie") != "sessionid=account" || got.Get("x-tt-token") != "token-value" || got.Get("x-tt-store-region") != "cn-gd" {
		t.Fatalf("credential headers = %v", got)
	}
}

func TestAuthTransportKeepsDeviceRegistrationAnonymous(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
	}))
	defer server.Close()

	transport := New()
	transport.SetCredentials("sessionid=account", "token-value", "cn-gd")
	_, err := transport.Do(context.Background(), &hgapi.Request{
		Method:  http.MethodPost,
		URL:     server.URL + "/service/2/device_register/",
		Headers: map[string]string{"Cookie": "guest"},
	})
	if err != nil {
		t.Fatalf("Do err = %v", err)
	}
	if got.Get("Cookie") != "guest" || got.Get("x-tt-token") != "" || got.Get("x-tt-store-region") != "" {
		t.Fatalf("registration headers = %v", got)
	}
	if strings.Contains(got.Get("Cookie"), "sessionid") {
		t.Fatalf("registration request used account cookie")
	}
}
