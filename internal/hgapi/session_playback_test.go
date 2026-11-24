package hgapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"myguo/internal/sign"
)

func wireInt(tag, n uint64) []byte { return binary.AppendUvarint(binary.AppendUvarint(nil, tag<<3), n) }
func wireBytes(tag uint64, p []byte) []byte {
	return append(binary.AppendUvarint(binary.AppendUvarint(nil, tag<<3|2), uint64(len(p))), p...)
}

func sdiResponse(t *testing.T, token string, status int64) *Response {
	t.Helper()
	payload := append(wireBytes(1, []byte(token)), wireInt(2, uint64(status)*2)...)
	request, err := sign.EncodeSDI(payload, 1795492800123)
	if err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	for len(request) > 0 {
		key, n := binary.Uvarint(request)
		request = request[n:]
		value, n := binary.Uvarint(request)
		request = request[n:]
		if key&7 == 2 {
			if key>>3 == 4 {
				encrypted = request[:value]
			}
			request = request[value:]
		}
	}
	response := append(wireInt(1, 0x40240624), wireInt(2, 2)...)
	response = append(response, wireBytes(6, encrypted)...)
	return &Response{StatusCode: 200, Body: response}
}

func sessionTestClient(t *testing.T, transport Transport) *Client {
	c := nativeTestClient(t, transport)
	c.sessions.enabled = true
	c.sessions.origin = "https://session.invalid"
	return c
}

func TestSessionPlaybackAcquiresAndBindsActualGzip(t *testing.T) {
	const id = "7100000000000201"
	var exchanges, plays int
	c := sessionTestClient(t, testTransport{call: func(r *Request) (*Response, error) {
		u, _ := url.Parse(r.URL)
		if u.Path == sessionTokenPath {
			if strings.Contains(r.Headers["Cookie"], "sessionid=account") || r.Headers["x-tt-token"] != "" {
				t.Fatal("SDI exchange contains account credentials")
			}
			exchanges++
			if exchanges == 1 {
				return sdiResponse(t, "session-first", 202), nil
			}
			return sdiResponse(t, "session-ready", 200), nil
		}
		plays++
		if r.Headers["Content-Encoding"] != "gzip" || !r.CredentialsManaged || r.SkipAuth || r.Headers["x-tt-token"] != "saved-token" {
			t.Fatal("session request lost compression or credential ownership")
		}
		if u.Query().Get("version_code") != "74332" || !strings.Contains(u.RawQuery, "&&host_abi=") {
			t.Fatal("session profile/order mismatch")
		}
		if !strings.Contains(r.Headers["Cookie"], "install_id="+u.Query().Get("iid")) {
			t.Fatal("cookie and signed IID differ")
		}
		z, err := gzip.NewReader(bytes.NewReader(r.Body))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(z)
		z.Close()
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Biz map[string]any `json:"biz_param"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if value, ok := payload.Biz["disable_video_relate_book"]; !ok || value != false {
			t.Fatal("session playback option missing")
		}
		sum := md5.Sum(r.Body)
		if !strings.EqualFold(r.Headers["x-ss-stub"], hex.EncodeToString(sum[:])) {
			t.Fatal("signature hashes plaintext instead of wire bytes")
		}
		return modelResponse(id), nil
	}})
	for i := 0; i < 2; i++ {
		if _, err := c.ResolvePlayback(context.Background(), id, PlaybackOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if exchanges != 2 || plays != 2 {
		t.Fatalf("exchanges=%d plays=%d", exchanges, plays)
	}
}

func TestSessionEmptyResponseReacquiresOnceThenFallsBack(t *testing.T) {
	const id = "7100000000000201"
	for _, recover := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "session"}[recover], func(t *testing.T) {
			var exchanges, compressed, plain int
			c := sessionTestClient(t, testTransport{call: func(r *Request) (*Response, error) {
				u, _ := url.Parse(r.URL)
				if u.Path == sessionTokenPath {
					exchanges++
					return sdiResponse(t, strconvToken(exchanges), 200), nil
				}
				if r.Headers["Content-Encoding"] == "gzip" {
					compressed++
					if !recover || compressed == 1 {
						return &Response{StatusCode: 200}, nil
					}
				} else {
					plain++
				}
				return modelResponse(id), nil
			}})
			if _, err := c.ResolvePlayback(context.Background(), id, PlaybackOptions{}); err != nil {
				t.Fatal(err)
			}
			wantPlain := 1
			if recover {
				wantPlain = 0
			}
			if exchanges != 2 || compressed != 2 || plain != wantPlain {
				t.Fatalf("exchanges=%d session=%d legacy=%d", exchanges, compressed, plain)
			}
		})
	}
}

func strconvToken(n int) string { return strings.Repeat("fixture", n) }

func TestSessionAuthenticationStopsLegacyRecovery(t *testing.T) {
	var calls int
	c := sessionTestClient(t, testTransport{call: func(r *Request) (*Response, error) { calls++; return &Response{StatusCode: 403}, nil }})
	_, err := c.ResolvePlayback(context.Background(), "7100000000000201", PlaybackOptions{})
	if !errors.Is(err, ErrAuthenticationRequired) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

type blockingSDITransport struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (s *blockingSDITransport) Do(ctx context.Context, r *Request) (*Response, error) {
	s.calls.Add(1)
	select {
	case s.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
		return nil, ErrTransport
	}
}

func TestSessionAcquireCoalescesAndWaiterCanCancel(t *testing.T) {
	sender := &blockingSDITransport{started: make(chan struct{}, 1), release: make(chan struct{})}
	c := sessionTestClient(t, sender)
	p, _ := c.playbackIdentity(context.Background())
	s := c.bindPlaybackSession(p)
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	owner := make(chan error, 1)
	go func() { _, err := c.acquirePlaybackSession(ownerCtx, s); owner <- err }()
	<-sender.started
	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	cancelWaiter()
	_, err := c.acquirePlaybackSession(waiterCtx, s)
	if !errors.Is(err, context.Canceled) || sender.calls.Load() != 1 {
		t.Fatal("waiter issued another exchange")
	}
	cancelOwner()
	if err := <-owner; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	c.sessions.mu.Lock()
	entry := c.sessions.entries[s.key]
	if !entry.retryAt.IsZero() || entry.loading != nil {
		t.Fatal("canceled acquisition left a cooldown or flight")
	}
	c.sessions.mu.Unlock()
}

func TestManualRetryDropsDetachedSessionCompletion(t *testing.T) {
	const id = "7100000000000201"
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	c := sessionTestClient(t, testTransport{call: func(r *Request) (*Response, error) {
		once.Do(func() { close(started) })
		<-release
		return sdiResponse(t, "late-session", 200), nil
	}})
	p, _ := c.playbackIdentity(context.Background())
	s := c.bindPlaybackSession(p)
	done := make(chan struct{})
	go func() { c.acquirePlaybackSession(context.Background(), s); close(done) }()
	<-started
	c.RetryPlaybackNow(id)
	close(release)
	<-done
	c.sessions.mu.Lock()
	defer c.sessions.mu.Unlock()
	if len(c.sessions.entries) != 0 {
		t.Fatal("old exchange restored invalidated session")
	}
}

func TestSessionRequestDeadlineAndInvalidOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := sessionTestClient(t, testTransport{call: func(*Request) (*Response, error) { t.Fatal("canceled request was sent"); return nil, nil }})
	if _, err := c.ResolveSessionPlayback(ctx, "7100000000000201", PlaybackOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := NewClient(Options{Transport: testTransport{}, ProfileStore: &testProfiles{}, SessionOrigin: "file:///tmp/session"}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatal("accepted invalid SDI origin")
	}
}
