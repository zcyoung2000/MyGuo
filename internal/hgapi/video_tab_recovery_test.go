package hgapi

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"myguo/internal/sign"
	"myguo/internal/upstream"
)

func TestEmptyPostRecoversWithVideoTabAndReusesWorkingRoute(t *testing.T) {
	const id = "7100000000000201"
	var paths []string
	c := nativeTestClient(t, testTransport{call: func(r *Request) (*Response, error) {
		u, _ := url.Parse(r.URL)
		paths = append(paths, r.Method+" "+u.Path)
		if r.Method == "POST" {
			return &Response{StatusCode: 200}, nil
		}
		if r.Method != "GET" {
			t.Fatal("registered a replacement device before trying a working GET route")
		}
		if u.Path != VideoTabPath || u.Query().Get("video_id") != id || u.Query().Get("version_code") != "74332" {
			t.Fatal("GET did not use the reference episode route/profile")
		}
		if !r.SkipAuth || !r.CredentialsManaged || r.Headers["Cookie"] != "" || r.Headers["x-tt-token"] != "" || len(r.Body) != 0 {
			t.Fatal("GET leaked account state or borrowed the POST body")
		}
		if r.Headers["x-medusa"] == "" || !strings.Contains(u.RawQuery, "from_video_id=&_rticket=") {
			t.Fatal("GET lost its ordered signed query")
		}
		return modelResponse(id), nil
	}})
	c.deviceResets = 2
	for range 2 {
		pb, err := c.ResolvePlayback(context.Background(), id, PlaybackOptions{})
		if err != nil || pb == nil || len(pb.Streams) != 1 {
			t.Fatalf("recovered playback = %v, %v", pb, err)
		}
	}
	want := []string{"POST " + PlaybackPath, "GET " + VideoTabPath, "GET " + VideoTabPath}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("working GET route was not reused: %v", paths)
	}
}

func TestVideoTabFailureReturnsToPostWithoutCyclingRoutes(t *testing.T) {
	const id = "7100000000000201"
	var paths []string
	c := nativeTestClient(t, testTransport{call: func(r *Request) (*Response, error) {
		paths = append(paths, r.Method)
		if r.Method == "GET" {
			return &Response{StatusCode: 200}, nil
		}
		return modelResponse(id), nil
	}})
	c.setVideoTabPreference(true)
	for range 2 {
		if _, err := c.ResolvePlayback(context.Background(), id, PlaybackOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(paths, []string{"GET", "POST", "POST"}) {
		t.Fatalf("failed GET preference survived or routes cycled: %v", paths)
	}
}

func TestCanceledPlaybackNeverStartsVideoTabRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var gets atomic.Int32
	c := nativeTestClient(t, testTransport{call: func(r *Request) (*Response, error) {
		if r.Method == "GET" {
			gets.Add(1)
		}
		cancel()
		return &Response{StatusCode: 200}, nil
	}})
	_, err := c.ResolvePlayback(ctx, "7100000000000201", PlaybackOptions{})
	if !errors.Is(err, context.Canceled) || gets.Load() != 0 {
		t.Fatalf("canceled playback started recovery: %v, GETs=%d", err, gets.Load())
	}
}

func TestPreferredAnonymousVideoTabDenialReturnsToAuthenticatedPost(t *testing.T) {
	const id = "7100000000000201"
	var paths []string
	c := nativeTestClient(t, testTransport{call: func(r *Request) (*Response, error) {
		paths = append(paths, r.Method)
		if r.Method == "GET" {
			return &Response{StatusCode: 403}, nil
		}
		if r.Headers["x-tt-token"] != "saved-token" {
			t.Fatal("POST lost its own authenticated session")
		}
		return modelResponse(id), nil
	}})
	c.setVideoTabPreference(true)
	if _, err := c.ResolvePlayback(context.Background(), id, PlaybackOptions{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"GET", "POST"}) {
		t.Fatalf("anonymous route denial did not reach authenticated POST: %v", paths)
	}
}

func TestVideoTabRejectsDifferentEpisodeBeforeRememberingDevice(t *testing.T) {
	c := nativeTestClient(t, testTransport{call: func(*Request) (*Response, error) {
		return modelResponse("7100000000000999"), nil
	}})
	_, err := c.ResolveVideoTab(context.Background(), "7100000000000201", PlaybackOptions{})
	if !errors.Is(err, ErrVideoTabEpisodeMissing) || c.tabDevice != nil || !c.preferTabUntil.IsZero() {
		t.Fatalf("different episode was accepted or cached: %v", err)
	}
}

func TestVideoTabTriesNextHostAfterRecoverableFailure(t *testing.T) {
	const id = "7100000000000201"
	for _, failure := range []*Response{
		{StatusCode: 200},
		{StatusCode: 500},
		{StatusCode: 200, Body: []byte(`{"code":110001,"message":"retry"}`)},
	} {
		t.Run(strconv.Itoa(failure.StatusCode)+"/"+string(failure.Body), func(t *testing.T) {
			var hosts []string
			c := nativeTestClient(t, testTransport{call: func(r *Request) (*Response, error) {
				u, _ := url.Parse(r.URL)
				hosts = append(hosts, u.Host)
				if u.Host == "first.invalid" {
					return failure, nil
				}
				if !r.SkipAuth || r.Headers["Cookie"] != "" || r.Headers["x-tt-token"] != "" {
					t.Fatal("fallback host inherited account credentials")
				}
				return modelResponse(id), nil
			}})
			pb, err := c.videoTabWith(context.Background(), &DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002"}, id,
				[]string{"https://first.invalid", "https://second.invalid"})
			if err != nil || pb == nil || !reflect.DeepEqual(hosts, []string{"first.invalid", "second.invalid"}) {
				t.Fatalf("fallback = %v, playback=%t, hosts=%v", err, pb != nil, hosts)
			}
		})
	}
}

func TestVideoTabStopsHostFallbackForTerminalResponse(t *testing.T) {
	const id = "7100000000000201"
	for _, response := range []*Response{
		{StatusCode: 403},
		{StatusCode: 429},
		{StatusCode: 200, Body: []byte(`{"code":4000,"message":"invalid parameter"}`)},
		modelResponse("7100000000000999"),
	} {
		calls := 0
		c := nativeTestClient(t, testTransport{call: func(*Request) (*Response, error) {
			calls++
			return response, nil
		}})
		_, err := c.videoTabWith(context.Background(), &DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002"}, id,
			[]string{"https://first.invalid", "https://second.invalid"})
		if err == nil || calls != 1 {
			t.Fatalf("terminal response retried: err=%v, calls=%d", err, calls)
		}
	}
}

func TestVideoTabTerminalResponseDoesNotTryOtherDevices(t *testing.T) {
	calls := 0
	c := nativeTestClient(t, testTransport{call: func(*Request) (*Response, error) {
		calls++
		return &Response{StatusCode: 403}, nil
	}})
	c.tabDevice = &DeviceProfile{DeviceID: "7100000000000011", InstallID: "7100000000000012"}
	c.playDevice = &DeviceProfile{DeviceID: "7100000000000021", InstallID: "7100000000000022"}
	_, err := c.ResolveVideoTab(context.Background(), "7100000000000201", PlaybackOptions{})
	if !errors.Is(err, ErrAuthenticationRequired) || calls != 1 {
		t.Fatalf("terminal response cycled devices: err=%v, calls=%d", err, calls)
	}
}

type tabBlockingTransport struct {
	started chan struct{}
	release chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
	once    sync.Once
}

func (s *tabBlockingTransport) Do(_ context.Context, _ *Request) (*Response, error) {
	n := s.active.Add(1)
	defer s.active.Add(-1)
	for old := s.peak.Load(); n > old; old = s.peak.Load() {
		if s.peak.CompareAndSwap(old, n) {
			break
		}
	}
	if n == 3 {
		s.once.Do(func() { close(s.started) })
	}
	<-s.release // Deliberately models a transport that has not honored cancellation yet.
	return &Response{StatusCode: 200}, nil
}

func TestVideoTabBatchBoundsConcurrencyAndDoesNotWaitForCanceledTransport(t *testing.T) {
	transport := &tabBlockingTransport{started: make(chan struct{}), release: make(chan struct{})}
	c := nativeTestClient(t, transport)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer close(transport.release)
	done := make(chan error, 1)
	go func() {
		_, err := c.VideoTabModels(ctx, []string{"7100000000000201", "7100000000000202", "7100000000000203", "7100000000000204", "7100000000000205"}, PlaybackOptions{})
		done <- err
	}()
	select {
	case <-transport.started:
	case <-time.After(2 * time.Second):
		t.Fatal("GET workers did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || transport.peak.Load() != 3 {
			t.Fatalf("cancel/concurrency = %v / %d", err, transport.peak.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("canceled GET waited for stalled transport")
	}
}

func TestPlaybackHostFailureDoesNotDiscardAnotherHostsValidModel(t *testing.T) {
	for _, status := range []int{500, 101000} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			const id = "7100000000000201"
			failed := make(chan struct{})
			c := nativeTestClient(t, testTransport{call: func(r *Request) (*Response, error) {
				u, _ := url.Parse(r.URL)
				if u.Host == upstream.DefaultPlaybackHosts[0] {
					close(failed)
					if status == 500 {
						return &Response{StatusCode: 500}, nil
					}
					return &Response{StatusCode: 200, Body: []byte(`{"code":101000}`)}, nil
				}
				<-failed
				time.Sleep(25 * time.Millisecond)
				return modelResponse(id), nil
			}})
			c.apiOrigin = "https://" + upstream.DefaultPlaybackHosts[0]
			body, _ := PlaybackBody([]string{id}, 1004)
			models, err := c.racePlaybackHosts(context.Background(), &DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002"}, []string{id}, body, sign.Full, false, "")
			if err != nil || models[id] == nil {
				t.Fatalf("a fast host failure canceled the valid response: %v", err)
			}
		})
	}
}
