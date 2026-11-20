package playback

import (
	"context"
	"errors"
	"testing"
	"time"

	"myguo/internal/avkit"
	"myguo/internal/hgapi"
	"myguo/internal/ipc"
)

func TestTransientSourceFailureRecoversQuietlyWithBoundedAttempts(t *testing.T) {
	b := &testBackend{}
	b.resolve = func(_ context.Context, id string, n int, q string) (*ipc.PlaybackDTO, error) {
		b.mu.Lock()
		count := 0
		for _, r := range b.requests {
			if r.SeriesID == id && r.EpisodeNum == n {
				count++
			}
		}
		b.mu.Unlock()
		if id == "A" && n == 1 && count == 1 {
			return nil, ipc.ErrPlaybackAccessLimited
		}
		return source(id, n, q), nil
	}
	c, _ := newControllerTest(t, b)
	c.Play("A", 1, 0, 1)
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.requests) > 0 })
	barrier(t, c)
	if s := c.Snapshot(); s.Error != "" || !s.Loading {
		t.Fatalf("transient failure exposed instead of recovering: %+v", s)
	}
	waitSource(t, c, "A", 1)
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for _, r := range b.requests {
		if r.SeriesID == "A" && r.EpisodeNum == 1 {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("retry count=%d", count)
	}
}

func TestNavigatingAwayDoesNotRetryFailedPrefetch(t *testing.T) {
	b := &testBackend{resolve: func(_ context.Context, id string, n int, q string) (*ipc.PlaybackDTO, error) {
		if id == "A" {
			return nil, errors.New("offline")
		}
		return source(id, n, q), nil
	}}
	c, _ := newControllerTest(t, b)
	c.ShowFeed("")
	eventually(t, func() bool { return c.Snapshot().Book.ID == "A" })
	c.Command("next", nil)
	waitSource(t, c, "B", 1)
	c.post(func() { c.tick(time.Now().Add(time.Minute)) })
	barrier(t, c)
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for _, r := range b.requests {
		if r.SeriesID == "A" && r.EpisodeNum == 1 {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("offscreen item spent retry budget: %d", count)
	}
}

func TestRemovedRecommendationIsSkippedWithoutRetry(t *testing.T) {
	b := &testBackend{restore: &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "removed"}, {ID: "good"}}}, feed: &ipc.RecommendFeedResult{}, resolve: func(_ context.Context, id string, n int, q string) (*ipc.PlaybackDTO, error) {
		if id == "removed" {
			return nil, hgapi.ErrContentUnavailable
		}
		return source(id, n, q), nil
	}}
	c, _ := newControllerTest(t, b)
	c.ShowFeed("")
	waitSource(t, c, "good", 1)
	if s := c.Snapshot(); s.Error != "" || s.Book.ID != "good" {
		t.Fatalf("removed work blocked feed: %+v", s)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for _, r := range b.requests {
		if r.SeriesID == "removed" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("removed work retried: %d", count)
	}
}

func TestStartingAtZeroDoesNotDiscardBufferedPipelineWithSeek(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.Play("A", 1, 0, 1)
	s := waitSource(t, c, "A", 1)
	e := s.Engine.(*testEngine)
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.seeks) != 0 {
		t.Fatalf("startup flushed pipeline: %v", e.seeks)
	}
}

func TestFirstFrameWithoutClockKeepsLoadingAndDefersPreload(t *testing.T) {
	c := New(&testBackend{}, func() (avkit.Player, error) { return &testEngine{holdClock: true}, nil }, "", Hooks{})
	t.Cleanup(c.Close)
	c.Play("A", 1, 0, 1)
	s := waitSource(t, c, "A", 1)
	if !s.Loading || preloadAdmission(s, false) {
		t.Fatal("first frame at zero admitted speculative media before playback")
	}
	e := s.Engine.(*testEngine)
	e.mu.Lock()
	e.status.Position = .2
	e.mu.Unlock()
	eventually(t, func() bool { return !c.Snapshot().Loading })
}

func TestBufferedFirstFrameReopensCachedHLSBeforeWaitingForFullDownload(t *testing.T) {
	c, s, e, status := recoveryFixture()
	s.playStarted = false
	status.Position, status.Buffered, status.Playing = 0, 12, false
	c.state.Status = status
	now := time.Now()
	c.recoverStall(s, status, now)
	if c.recoverStall(s, status, now.Add(700*time.Millisecond)) || e.plays != 1 {
		t.Fatal("buffered startup did not get one native resume")
	}
	if !c.recoverStall(s, status, now.Add(2*time.Second)) || e.url != "hls" || len(e.loads) != 2 || !s.startReloaded {
		t.Fatalf("startup waited for a full download: source=%s", e.url)
	}
	if len(e.seeks) != 0 || !c.state.Loading {
		t.Fatalf("startup recovery discarded its zero-position pipeline: %v", e.seeks)
	}
	// A second stalled start exhausts this one local reload and uses the
	// existing transport fallback. It cannot become an infinite reload loop.
	s.first = true
	c.recoverStall(s, status, now.Add(3*time.Second))
	if !c.recoverStall(s, status, now.Add(6*time.Second)) || e.url != "full" {
		t.Fatal("repeated startup spun instead of reaching the fallback")
	}
}

func TestPlayAfterExhaustedSourceFailureStartsAFreshQuietAttempt(t *testing.T) {
	b := &testBackend{}
	b.resolve = func(_ context.Context, id string, n int, q string) (*ipc.PlaybackDTO, error) {
		b.mu.Lock()
		attempts := 0
		for _, request := range b.requests {
			if request.SeriesID == "A" && request.EpisodeNum == 1 {
				attempts++
			}
		}
		b.mu.Unlock()
		if id == "A" && n == 1 && attempts <= 3 {
			return nil, ipc.ErrPlaybackAccessLimited
		}
		return source(id, n, q), nil
	}
	c, _ := newControllerTest(t, b)
	c.Play("A", 1, 0, 1)
	eventually(t, func() bool { return c.Snapshot().Error != "" })
	c.Command("play", nil)
	s := waitSource(t, c, "A", 1)
	if s.Paused || s.Error != "" || !s.Status.Playing {
		t.Fatal("play control could not recover after removing the error card")
	}
}
