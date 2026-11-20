package hgapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type sessionTestTransport func(*http.Request) (*http.Response, error)

func (f sessionTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func apiTestResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func testSession() Session {
	return Session{DeviceID: "710000000000001", InstallID: "710000000000002", CDID: "cdid", OpenUDID: "openudid", Cookies: "sessionid=original", Token: "token-original", UserAgent: "fixture-agent", StoreRegion: "cn"}
}

func TestSessionRequestsKeepBoundCredentialsAndTypedRoutes(t *testing.T) {
	session := testSession()
	calls := 0
	httpClient := &http.Client{Transport: sessionTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Cookie") != "sessionid=original" || r.Header.Get("x-tt-token") != "token-original" || r.Header.Get("User-Agent") != "fixture-agent" {
			t.Fatal("request changed its bound identity")
		}
		q := r.URL.Query()
		if q.Get("device_id") != "710000000000001" || q.Get("iid") != "710000000000002" || r.Header.Get("x-gorgon") == "" {
			t.Fatal("missing shared device/signing policy")
		}
		switch r.URL.Path {
		case "/reading/bookapi/read_progress/get/v":
			if r.Method != "POST" || r.Header.Get("Content-Encoding") != "gzip" {
				t.Fatal("single-series progress request changed")
			}
			z, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			defer z.Close()
			var payload struct {
				BookIDs map[string][]int64 `json:"book_ids"`
			}
			if err := json.NewDecoder(z).Decode(&payload); err != nil || len(payload.BookIDs["2"]) != 1 || payload.BookIDs["2"][0] != 710000000000003 {
				t.Fatal("history scope changed")
			}
			return apiTestResponse(`{"code":0,"data":[],"video_data":[]}`), nil
		case "/reading/user/subscribe/list/v1/":
			if q.Get("tab_type") != "13" || q.Get("is_online") != "true" {
				t.Fatal("reservation query became public calendar")
			}
			return apiTestResponse(`{"code":0,"data":{"subscribe_items":[]}}`), nil
		}
		t.Fatalf("unexpected endpoint %s", r.URL.Path)
		return nil, nil
	})}
	api := NewSessionClient(httpClient, session)
	session.Cookies, session.Token = "sessionid=replacement", "new-token"
	if _, err := api.History(context.Background(), 0, "710000000000003"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Reservations(context.Background(), 0, true); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("unexpected implicit retry")
	}
}

func TestMutationsSignCompressedBytesAndExposeParameterErrorWithoutReplay(t *testing.T) {
	calls := 0
	api := NewSessionClient(&http.Client{Transport: sessionTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		wire, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		sum := md5.Sum(wire)
		if r.Header.Get("Content-Encoding") != "gzip" || !strings.EqualFold(r.Header.Get("x-ss-stub"), hex.EncodeToString(sum[:])) {
			t.Fatal("signature is not bound to compressed body")
		}
		z, err := gzip.NewReader(bytes.NewReader(wire))
		if err != nil {
			t.Fatal(err)
		}
		defer z.Close()
		var body struct {
			Updates []struct {
				Index    int   `json:"vid_index"`
				Position int64 `json:"current_play_position"`
			} `json:"update_datas"`
		}
		if err := json.NewDecoder(z).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Updates) != 1 || body.Updates[0].Index != 2 || body.Updates[0].Position != 12500 {
			t.Fatalf("progress units changed: %+v", body)
		}
		return apiTestResponse(`{"code":100103,"message":"parameter invalid"}`), nil
	})}, testSession())
	err := api.UpdateHistory(context.Background(), []ProgressRecord{{SeriesID: "710000000000003", EpisodeNum: 3, PositionSec: 12.5}}, []string{"710000000000004"}, false)
	if !errors.Is(err, ErrParameterInvalid) || calls != 1 {
		t.Fatalf("mutation lost batch-split signal or replayed: %v calls=%d", err, calls)
	}
}

func TestGuestCannotWriteInteractionsOrUserLibrary(t *testing.T) {
	session := testSession()
	session.Guest = true
	session.Cookies = "install_id=guest; ttreq=guest"
	api := NewSessionClient(&http.Client{Transport: sessionTestTransport(func(*http.Request) (*http.Response, error) { t.Fatal("guest write reached network"); return nil, nil })}, session)
	if err := api.UpdateFavorite(context.Background(), "710000000000003", true); !errors.Is(err, ErrAuthenticationRequired) {
		t.Fatal(err)
	}
	if _, err := api.SendDanmaku(context.Background(), "710000000000003", "710000000000004", "hello", 0, ""); err == nil {
		t.Fatal("guest ticket counted as login")
	}
}

func TestProgressUploadUsesProtocolFlagsAndPlaybackMilliseconds(t *testing.T) {
	calls := 0
	api := NewSessionClient(&http.Client{Transport: sessionTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/reading/bookapi/read_progress/upload/v" {
			t.Fatalf("unexpected progress endpoint: %s", r.URL.Path)
		}
		z, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		defer z.Close()
		var payload struct {
			Books []map[string]any `json:"books"`
		}
		if err := json.NewDecoder(z).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Books) != 1 {
			t.Fatalf("unexpected progress records: %d", len(payload.Books))
		}
		row := payload.Books[0]
		if row["progress_type"] != float64(0) || row["check_timestamp"] != false {
			t.Fatalf("protocol flags replaced by a clock: progress_type=%v check_timestamp=%v", row["progress_type"], row["check_timestamp"])
		}
		if row["current_play_time"] != float64(12500) || row["player_cumulative_total_duration"] != float64(12500) || row["vid_index"] != float64(2) {
			t.Fatal("progress position or episode index changed")
		}
		return apiTestResponse(`{"code":0}`), nil
	})}, testSession())
	if err := api.UploadProgress(context.Background(), []ProgressRecord{{SeriesID: "710000000000003", EpisodeNum: 3, PositionSec: 12.5}}, []string{"710000000000004"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("progress writes = %d, want one", calls)
	}
}

func TestSessionTransportFailureNeverReplaysMutationOrExposesURL(t *testing.T) {
	calls := 0
	api := NewSessionClient(&http.Client{Transport: sessionTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("signed-url-with-token-secret")
	})}, testSession())
	err := api.UpdateFavorite(context.Background(), "710000000000003", true)
	if err == nil || calls != 1 || strings.Contains(err.Error(), "token-secret") {
		t.Fatalf("unsafe mutation error/retry: %v calls=%d", err, calls)
	}
}

func TestExchangeRejectsOversizedResponseAndClosesBody(t *testing.T) {
	closed := false
	client := &http.Client{Transport: sessionTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &closeRecorder{Reader: io.LimitReader(zeroReader{}, responseLimit+1), closed: &closed}}, nil
	})}
	_, err := Exchange(context.Background(), client, &Request{URL: "https://fixture.invalid/api"})
	if err == nil || !closed {
		t.Fatal("oversized response was accepted or body leaked")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type closeRecorder struct {
	io.Reader
	closed *bool
}

func (c *closeRecorder) Close() error { *c.closed = true; return nil }

func TestSessionReadsDecodeGzipWithoutTreatingBusinessDataAsTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		z := gzip.NewWriter(w)
		defer z.Close()
		_, _ = io.WriteString(z, `{"code":0,"data":{"data_list":[]}}`)
	}))
	defer server.Close()
	api := NewSessionClient(server.Client(), testSession())
	body, err := api.call(context.Background(), endpoint{"fixture", server.URL, "/history", "GET", getSigned, 4 << 20}, api.devicePairs(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	page, err := ParseHistoryPage(body)
	if err != nil || page.Items == nil {
		t.Fatalf("gzip response lost empty successful snapshot: %v", err)
	}
}
