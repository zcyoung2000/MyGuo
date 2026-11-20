package playback

import (
	"context"
	"errors"
	"testing"
	"time"

	"myguo/internal/ipc"
)

func TestFeedNavigationStartsEachSelectedVideoAfterPause(t *testing.T) {
	c, r := newControllerTest(t, &testBackend{})
	c.ShowFeed("推荐")
	waitSource(t, c, "A", 1)
	for _, step := range []struct{ command, id string }{{"next", "B"}, {"next", "C"}, {"prev", "B"}, {"prev", "A"}} {
		c.Command("pause", nil)
		barrier(t, c)
		c.Command(step.command, nil)
		s := waitSource(t, c, step.id, 1)
		if s.Paused || !s.Status.Playing {
			t.Fatalf("%s selected a paused video: %+v", step.command, s.Status)
		}
		if _, audible := r.active(); audible != 1 {
			t.Fatalf("feed selection has %d audible players", audible)
		}
	}
}

func TestDecoderStopsDoNotBecomeInlinePauseIntent(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.ShowFeed("推荐")
	s := waitSource(t, c, "A", 1)
	e := s.Engine.(*testEngine)
	e.mu.Lock()
	e.status.Playing, e.status.Buffering = false, false
	e.mu.Unlock()
	barrier(t, c)
	c.post(func() { c.tick(time.Now().Add(200 * time.Millisecond)) })
	barrier(t, c)
	if c.Snapshot().Paused {
		t.Fatal("decoder's zero rate became a user pause")
	}
}

func TestPictureInPicturePauseAndPlayRemainSupported(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.Play("A", 1, 0, 1)
	s := waitSource(t, c, "A", 1)
	e := s.Engine.(*testEngine)
	e.mu.Lock()
	e.status.PictureInPicture, e.status.Playing = true, false
	e.mu.Unlock()
	eventually(t, func() bool { return c.Snapshot().Paused })
	e.mu.Lock()
	e.status.Playing = true
	e.mu.Unlock()
	eventually(t, func() bool { return !c.Snapshot().Paused })
}

func TestFailedReadyFrameCannotEraseErrorOrMarkReplacementReady(t *testing.T) {
	for _, full := range []string{"", "full:A"} {
		t.Run(full, func(t *testing.T) {
			b := &testBackend{resolve: func(_ context.Context, id string, n int, q string) (*ipc.PlaybackDTO, error) {
				dto := source(id, n, q)
				dto.FullStreamURL = full
				return dto, nil
			}}
			c, _ := newControllerTest(t, b)
			c.Play("A", 1, 0, 1)
			s := waitSource(t, c, "A", 1)
			e := s.Engine.(*testEngine)
			result := make(chan State, 1)
			c.post(func() {
				c.current.first = false
				e.mu.Lock()
				e.status.Ready, e.status.Error = true, "resource unavailable"
				e.mu.Unlock()
				c.tick(time.Now())
				result <- c.state
			})
			got := <-result
			if full == "" && (got.Error == "" || got.Loading) {
				t.Fatal("stale ready frame erased the exhausted error")
			}
			if full == "" {
				c.post(func() { c.tick(time.Now()); result <- c.state })
				if next := <-result; next.Error == "" {
					t.Fatal("later stale ready frame erased the exhausted error")
				}
			}
			if full != "" && !got.Loading {
				t.Fatal("old frame marked the replacement source ready")
			}
		})
	}
}

func TestPlaybackAccessFailureStaysRetryableAndDoesNotSkipFeed(t *testing.T) {
	b := &testBackend{resolve: func(_ context.Context, id string, n int, q string) (*ipc.PlaybackDTO, error) {
		if id == "A" {
			return nil, errors.Join(ipc.ErrPlaybackAccessLimited, errors.New("empty response"))
		}
		return source(id, n, q), nil
	}}
	c, _ := newControllerTest(t, b)
	c.ShowFeed("推荐")
	eventually(t, func() bool { return c.Snapshot().Error != "" })
	s := c.Snapshot()
	if s.Book.ID != "A" || s.Error != "播放信息访问受限，请稍后重试" || s.Engine != nil {
		t.Fatalf("access failure misclassified or skipped: %+v", s)
	}
	b.mu.Lock()
	attempts := 0
	for _, request := range b.requests {
		if request.SeriesID == "A" && request.EpisodeNum == 1 {
			attempts++
		}
	}
	b.mu.Unlock()
	if attempts != 3 {
		t.Fatalf("source recovery was not bounded: attempts=%d", attempts)
	}
	c.Command("next", nil)
	s = waitSource(t, c, "B", 1)
	if s.Paused || !s.Status.Playing {
		t.Fatal("failed item left next recommendation paused")
	}
}

func TestSilentStoppedDecoderGetsOneResumeBeforeFallback(t *testing.T) {
	c, s, e, status := recoveryFixture()
	status.Playing, status.Buffering = false, false
	status.Buffered = status.Position
	now := time.Now()
	c.recoverStall(s, status, now)
	c.recoverStall(s, status, now.Add(3*time.Second))
	c.recoverStall(s, status, now.Add(4*time.Second))
	if e.plays != 1 || e.url != "hls" {
		t.Fatal("silent stopped decoder did not receive one resume attempt")
	}
	if !c.recoverStall(s, status, now.Add(8*time.Second)) || e.url != "full" {
		t.Fatal("silent stopped decoder remained stuck")
	}
}
