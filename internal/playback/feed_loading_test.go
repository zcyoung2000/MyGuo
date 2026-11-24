package playback

import (
	"context"
	"testing"
	"time"

	"myguo/internal/ipc"
)

func TestFeedSwipingPastUnresolvedItemClearsOutgoingFrame(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "loading", true: "failed"}[failed], func(t *testing.T) {
			b := &testBackend{resolve: func(ctx context.Context, id string, n int, q string) (*ipc.PlaybackDTO, error) {
				if id == "A" {
					return source(id, n, q), nil
				}
				if failed {
					return nil, ipc.ErrPlaybackAccessLimited
				}
				<-ctx.Done()
				return nil, ctx.Err()
			}}
			c, _ := newControllerTest(t, b)
			c.ShowFeed("")
			waitSource(t, c, "A", 1)
			c.Command("next", nil)
			barrier(t, c)
			if len(c.Snapshot().TransitionFrame) == 0 {
				t.Fatal("first swipe did not capture the playing item")
			}
			if failed {
				eventually(t, func() bool { return c.Snapshot().Error != "" })
			}
			c.Command("next", nil)
			barrier(t, c)
			if s := c.Snapshot(); s.Book.ID != "C" || len(s.TransitionFrame) != 0 {
				t.Fatalf("unresolved item carried an older screenshot into %q", s.Book.ID)
			}
		})
	}
}

func TestFeedLoadingSurfaceClickKeepsIntentAndSlowSourceAutoplays(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "request", true: "retry-delay"}[retry], func(t *testing.T) {
			release := make(chan struct{})
			b := &testBackend{}
			b.resolve = func(ctx context.Context, id string, n int, q string) (*ipc.PlaybackDTO, error) {
				if id == "A" && n == 1 {
					b.mu.Lock()
					attempts := 0
					for _, r := range b.requests {
						if r.SeriesID == id && r.EpisodeNum == n {
							attempts++
						}
					}
					b.mu.Unlock()
					if retry && attempts == 1 {
						return nil, ipc.ErrPlaybackAccessLimited
					}
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return source(id, n, q), nil
			}
			c, _ := newControllerTest(t, b)
			c.ShowFeed("")
			eventually(t, func() bool { return c.Snapshot().Book.ID == "A" && c.Snapshot().Loading })
			if retry {
				ready := make(chan bool, 1)
				eventually(t, func() bool {
					c.post(func() { ready <- c.current != nil && !c.current.retryAt.IsZero() })
					return <-ready
				})
			}
			for range 2 {
				c.Command("surface", nil)
				barrier(t, c)
				if s := c.Snapshot(); s.Paused || s.Error != "" || !s.Loading {
					t.Fatal("loading picture taps paused playback or interrupted recovery")
				}
			}
			close(release)
			waitSource(t, c, "A", 1)
			eventually(t, func() bool { return c.Snapshot().Status.Playing && !c.Snapshot().Paused })
		})
	}
}

func TestPausedFirstFrameDoesNotTimeoutAndResumeGetsFreshDeadline(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.Play("A", 1, 0, 1)
	visible := waitSource(t, c, "A", 1)
	e := visible.Engine.(*testEngine)
	e.mu.Lock()
	e.status.Ready, e.status.Displayed = false, false
	e.holdClock = true
	e.mu.Unlock()
	c.post(func() {
		s := c.current
		s.first, s.playStarted = false, false
		s.sourceStarted = time.Now().Add(-time.Minute)
		c.state.Loading = true
		c.setPaused(true)
		c.tick(time.Now())
		if s.fallback != 0 || c.state.Error != "" {
			t.Error("intentional pause triggered first-frame fallback")
		}
		c.setPaused(false)
		c.tick(time.Now())
		if s.fallback != 0 || c.state.Error != "" {
			t.Error("resume used the expired pre-pause deadline")
		}
		c.tick(s.sourceStarted.Add(c.sourceTimeout(s) + time.Second))
		if s.sourceURL != s.dto.FullStreamURL {
			t.Error("unpaused first-frame timeout stopped recovering")
		}
	})
	barrier(t, c)
}
