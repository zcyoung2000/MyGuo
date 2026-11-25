package playback

import (
	"context"
	"runtime"
	"testing"
	"time"

	"myguo/internal/ipc"
)

// The grace period is compiled in on Windows only. These tests assert the
// contract on both sides: Windows suppresses the promotion spinner until
// playback starts, with a bounded maximum; non-Windows never enters the grace.
func TestLoadingGracePlatformContract(t *testing.T) {
	now := time.Now()
	grace, since := beginLoadingGrace(true, now)

	if runtime.GOOS == "windows" {
		if !grace || since.IsZero() {
			t.Fatal("Windows promotion must start a loading grace window")
		}
		// Before the maximum window elapses the grace stays active.
		g2, s2 := expireLoadingGrace(grace, since, now.Add(loadingGraceWindow/2))
		if !g2 || s2.IsZero() {
			t.Fatal("grace must remain active before the maximum window elapses")
		}
		// After the maximum it clears so a stalled load still shows the spinner.
		g3, s3 := expireLoadingGrace(grace, since, now.Add(loadingGraceWindow+time.Millisecond))
		if g3 || !s3.IsZero() {
			t.Fatalf("grace must expire after the window, got grace=%t since=%v", g3, s3)
		}
	} else {
		if grace || !since.IsZero() {
			t.Fatal("non-Windows promotion must not start a loading grace")
		}
		g3, s3 := expireLoadingGrace(grace, since, now.Add(time.Hour))
		if g3 || !s3.IsZero() {
			t.Fatal("non-Windows grace must stay disabled")
		}
	}
}

func TestLoadingGraceOnlyForLoading(t *testing.T) {
	// A promotion that is already displaying a frame must not suppress the
	// spinner on any platform.
	grace, since := beginLoadingGrace(false, time.Now())
	if grace || !since.IsZero() {
		t.Fatalf("non-loading must not enter grace, got grace=%t since=%v", grace, since)
	}
}

func TestEndLoadingGraceClearsState(t *testing.T) {
	grace, since := endLoadingGrace()
	if grace || !since.IsZero() {
		t.Fatalf("ended grace must be clean, got grace=%t since=%v", grace, since)
	}
}

// On Windows the suppression must start with the selection, before the async
// PlayEpisode negotiation, and disappear once the new episode actually starts
// playing. Non-Windows keeps the original immediate-spinner behavior.
func TestPromotionGraceCoversResolveAndEndsOnPlayback(t *testing.T) {
	release := make(chan struct{})
	b := &testBackend{resolve: func(ctx context.Context, id string, num int, quality string) (*ipc.PlaybackDTO, error) {
		if num == 2 {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		dto := source(id, num, quality)
		dto.LastPosition = 0
		return dto, nil
	}}
	c, _ := newControllerTest(t, b)
	c.Play("series", 1, 0, 1)
	waitSource(t, c, "series", 1)
	eventually(t, func() bool { return c.Snapshot().Status.Ready })

	c.post(func() { c.selectSlot(ipc.SeriesCard{ID: "series"}, 2, -1, 1) })
	if runtime.GOOS == "windows" {
		eventually(t, func() bool {
			s := c.Snapshot()
			return s.Loading && s.LoadingGrace && !s.LoadingSince.IsZero()
		})
	} else {
		eventually(t, func() bool {
			s := c.Snapshot()
			return s.Loading && !s.LoadingGrace && s.LoadingSince.IsZero()
		})
	}
	close(release)
	waitPlayback(t, c, 2)
	eventually(t, func() bool {
		s := c.Snapshot()
		return !s.Loading && !s.LoadingGrace && s.LoadingSince.IsZero()
	})
}
