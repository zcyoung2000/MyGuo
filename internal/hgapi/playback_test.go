package hgapi

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"myguo/internal/sign"
)

type testProfiles struct {
	p      *DeviceProfile
	writes int
}

func (s *testProfiles) Load(context.Context) (*DeviceProfile, error) {
	if s.p == nil {
		return nil, ErrProfileNotFound
	}
	p := *s.p
	return &p, nil
}
func (s *testProfiles) Save(_ context.Context, p *DeviceProfile) error {
	s.writes++
	s.p = p
	return nil
}
func (s *testProfiles) Reset(context.Context) error { s.p = nil; return nil }

type testTransport struct {
	call func(*Request) (*Response, error)
}

func (t testTransport) Do(ctx context.Context, r *Request) (*Response, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return t.call(r)
}
func (t testTransport) Credentials() (string, string, string) {
	return "sessionid=account; install_id=old", "saved-token", "cn"
}
func modelResponse(id string) *Response {
	b, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{id: map[string]any{"video_model": `{"video_id":"opaque-media-id","video_duration":136.2,"video_list":{"v1":{"main_url":"https://media.invalid/a.mp4","video_meta":{"codec_type":"h264","definition":"720p"}}}}`}}})
	return &Response{StatusCode: 200, Body: b}
}
func nativeTestClient(t *testing.T, transport Transport) *Client {
	t.Helper()
	c, err := NewClient(Options{Transport: transport, ProfileStore: &testProfiles{p: &DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002"}}, APIOrigin: "https://test.invalid", Clock: func() time.Time { return time.Unix(1794820000, 123000000) }, DeviceResets: Int(0)})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestReferenceFullRequestBindsProfileBodyAndIID(t *testing.T) {
	const id = "7100000000000201"
	c := nativeTestClient(t, testTransport{call: func(r *Request) (*Response, error) {
		u, _ := url.Parse(r.URL)
		q := u.Query()
		if u.Path != PlaybackPath || q.Get("version_code") != "71332" || q.Get("version_name") != "7.4.3.32" || q.Get("channel") != "update_64" || q.Get("os_api") != "36" {
			t.Fatalf("incorrect reference profile: %s", u.Path)
		}
		var body struct {
			DR  string              `json:"dr_scene"`
			Map map[string][]string `json:"mixed_video_id_map"`
			Biz map[string]any      `json:"biz_param"`
		}
		_ = json.Unmarshal(r.Body, &body)
		if body.DR != "player" || len(body.Map["1004"]) != 1 || body.Map["1004"][0] != id || body.Biz["device_level"] != float64(4) || body.Biz["video_platform"] != float64(1024) {
			t.Fatalf("incorrect player body")
		}
		if r.Headers["Content-Encoding"] != "" || !bytes.HasPrefix(r.Body, []byte("{")) {
			t.Fatal("reference PlainBody was compressed")
		}
		sum := md5.Sum(r.Body)
		if !strings.EqualFold(r.Headers["x-ss-stub"], hex.EncodeToString(sum[:])) {
			t.Fatal("signature does not bind transmitted bytes")
		}
		medusa, err := base64.StdEncoding.DecodeString(r.Headers["x-medusa"])
		if err != nil || len(medusa) < 128 {
			t.Fatal("V5 signature missing")
		}
		if !r.CredentialsManaged || r.SkipAuth || !strings.Contains(r.Headers["Cookie"], "install_id="+q.Get("iid")) || r.Headers["x-tt-token"] != "saved-token" {
			t.Fatal("playback account and IID are inconsistent")
		}
		return modelResponse(id), nil
	}})
	pb, err := c.ResolvePlayback(context.Background(), id, PlaybackOptions{})
	if err != nil || pb.DurationSeconds != 136 {
		t.Fatalf("resolve: %v", err)
	}
}

func TestUnsignedModeSwitchDoesNotLeakAccount(t *testing.T) {
	const id = "7100000000000201"
	calls := 0
	var unsignedIID string
	c := nativeTestClient(t, testTransport{call: func(r *Request) (*Response, error) {
		calls++
		u, _ := url.Parse(r.URL)
		if !r.SkipAuth {
			return &Response{StatusCode: 200, Body: []byte(`{"code":110001,"message":"retry player mode"}`)}, nil
		}
		if r.Headers["Cookie"] != "" || r.Headers["x-tt-token"] != "" || r.Headers["x-medusa"] != "" || r.Headers["Content-Encoding"] != "" {
			t.Fatal("unsigned request carries account/signature")
		}
		if unsignedIID != "" && unsignedIID != u.Query().Get("iid") {
			t.Fatal("unsigned identity changed within its lifetime")
		}
		unsignedIID = u.Query().Get("iid")
		return modelResponse(id), nil
	}})
	mode := sign.None
	for i := 0; i < 2; i++ {
		if _, err := c.ResolvePlayback(context.Background(), id, PlaybackOptions{Mode: &mode}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 {
		t.Fatalf("good mode was not remembered: requests=%d", calls)
	}
}

func TestBatchParsingRequiresRequestedEpisodeAndWireModel(t *testing.T) {
	if _, err := ParseVideoModels(modelResponse("7100000000000999").Body, []string{"7100000000000201"}); !errors.Is(err, ErrAnonymousLimited) {
		t.Fatal("accepted another episode")
	}
	if _, err := ParseVideoModels([]byte(`{"data":{"7100000000000201":{"streams":[{"url":"https://media.invalid/a"}]}}}`), []string{"7100000000000201"}); !errors.Is(err, ErrAnonymousLimited) {
		t.Fatal("accepted local DTO as wire model")
	}
	if _, err := PlaybackBody(make([]string, 16), 1004); err == nil {
		t.Fatal("accepted over-sized request")
	}
}

func TestAuthenticationErrorStopsPlaybackRecovery(t *testing.T) {
	calls := 0
	c := nativeTestClient(t, testTransport{call: func(*Request) (*Response, error) { calls++; return &Response{StatusCode: 403}, nil }})
	_, err := c.ResolvePlayback(context.Background(), "7100000000000201", PlaybackOptions{})
	if !errors.Is(err, ErrAuthenticationRequired) || calls != 1 {
		t.Fatalf("authentication response retried: %v, requests=%d", err, calls)
	}
}
