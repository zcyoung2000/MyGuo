package playback

import (
	"context"
	"fmt"
	"testing"

	"myguo/internal/avkit"
	"myguo/internal/ipc"
)

func TestPreloadAdmissionProtectsPlaybackWithoutCancelingItsBufferedTail(t *testing.T) {
	for _, tt := range []struct {
		name                               string
		position, buffered, duration, rate float64
		admitted, buffering, paused, want  bool
	}{
		{"cold runway", 10, 14, 200, 1, false, false, false, false},
		{"ready runway", 10, 16, 200, 1, false, false, false, true},
		{"hysteresis keeps download", 10, 14, 200, 1, true, false, false, true},
		{"starving yields bandwidth", 10, 11, 200, 1, true, false, false, false},
		{"buffered last two seconds", 198, 200, 200, 1, false, false, false, true},
		{"buffering yields bandwidth", 10, 20, 200, 1, true, true, false, false},
		{"double speed insufficient", 10, 16, 200, 2, false, false, false, false},
		{"double speed ready", 10, 22, 200, 2, false, false, false, true},
		{"paused can prepare neighbors", 10, 11, 200, 1, false, false, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := State{Status: avkit.Status{Ready: true, Position: tt.position, Buffered: tt.buffered, Duration: tt.duration, Buffering: tt.buffering}, Preferences: Preferences{Rate: tt.rate}, Paused: tt.paused}
			if got := preloadAdmission(s, tt.admitted); got != tt.want {
				t.Fatalf("admission=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestPreloadWithReportedButZeroRangesStaysConservative(t *testing.T) {
	// A player that reports ranges (RangesUnsupported=false) but currently has
	// no buffered runway must still be conservative: only full cache, pause or
	// a genuine stall change its admission. Windows is covered separately via
	// RangesUnsupported, which carries the platform capability explicitly.
	s := State{Status: avkit.Status{Ready: true, Position: 10, Duration: 200}, Preferences: Preferences{Rate: 1}, Playback: &ipc.PlaybackDTO{}}
	if preloadAdmission(s, true) {
		t.Fatal("empty reported runway admitted background downloads")
	}
	s.Playback.IsFullCached = true
	if !preloadAdmission(s, false) {
		t.Fatal("complete local media blocked preloading")
	}
	s.Status.Buffering = true
	if preloadAdmission(s, true) {
		t.Fatal("buffering did not yield bandwidth")
	}
	s.Status.Buffering, s.Playback.IsFullCached, s.Paused = false, false, true
	if !preloadAdmission(s, false) {
		t.Fatal("paused media blocked preloading")
	}
}

func TestPreloadWithoutBufferedRangesAdmitsHealthyPlayback(t *testing.T) {
	base := State{Status: avkit.Status{Ready: true, Position: 10, Duration: 200, RangesUnsupported: true}, Preferences: Preferences{Rate: 1}, Playback: &ipc.PlaybackDTO{}}
	if !preloadAdmission(base, false) {
		t.Fatal("unknown buffered ranges blocked disk preload of a healthy stream")
	}
	if canWarmNeighbor(base) {
		t.Fatal("unknown buffered ranges must not open a native preroll decoder")
	}
	// Media Foundation reports Buffering/Loading for a growing HLS stream even
	// while it decodes fine. On a platform with no published ranges those are
	// transient noise: canceling on them tore the prefetch down seconds after
	// it started, so the next episode was never actually cached.
	transient := base
	transient.Status.Buffering = true
	if !preloadAdmission(transient, true) {
		t.Fatal("transient buffering canceled disk preload without ranges")
	}
	transient = base
	transient.Loading = true
	if !preloadAdmission(transient, true) {
		t.Fatal("transient loading canceled disk preload without ranges")
	}
	// A genuine failure must still yield bandwidth on every platform.
	stalled := base
	stalled.Error = "network"
	if preloadAdmission(stalled, true) {
		t.Fatal("playback error ignored even with unknown ranges")
	}
	stalled = base
	stalled.Status.Ready = false
	if preloadAdmission(stalled, true) {
		t.Fatal("unready player ignored even with unknown ranges")
	}
}

func TestRangesUnsupportedKeepsLookaheadOnDiskWithoutWarmingDecoder(t *testing.T) {
	b := &testBackend{}
	c, r := newControllerTestNoRanges(t, b)
	c.Play("A", 1, 0, 1)
	waitSource(t, c, "A", 1)
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, window := range b.preload {
			if len(window) == 2 && window[0].Number == 2 && window[1].Number == 3 {
				return true
			}
		}
		return false
	})
	// Disk window is populated, but no native decoder is opened for the
	// neighbor because MyGo cannot pre-decode without buffered ranges.
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.engines) != 1 {
		t.Fatalf("warmed %d native decoders without buffered ranges, want 1 (current only)", len(r.engines))
	}
}

func TestNoRangesKeepsPreloadWindowWhenRunwayDisappears(t *testing.T) {
	b := &testBackend{}
	// Windows always reports Buffered=0. The old rule read that as an empty
	// runway and cancelled the finished window; the new rule must keep the
	// lookahead on disk regardless of the reported value.
	c, _ := newControllerTestNoRanges(t, b)
	c.Play("A", 1, 0, 1)
	s := waitSource(t, c, "A", 1)
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.preload) > 0 && len(b.preload[len(b.preload)-1]) == 2
	})
	e := s.Engine.(*testEngine)
	e.mu.Lock()
	e.status.Buffered = e.status.Position
	e.mu.Unlock()
	c.post(func() { c.updatePreload() })
	barrier(t, c)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.preload) == 0 {
		t.Fatal("no preload window was submitted")
	}
	last := b.preload[len(b.preload)-1]
	if len(last) != 2 {
		t.Fatalf("preload window was cancelled when buffered runway dropped: last=%v", last)
	}
}

func TestRangedAdmissionStillCancelsWindowOnLostBudget(t *testing.T) {
	b := &testBackend{}
	// Platforms with published ranges keep the original behavior: when the
	// runway budget drops, submit an empty window to cancel disk prefetch.
	c, _ := newControllerTest(t, b)
	c.Play("A", 1, 0, 1)
	s := waitSource(t, c, "A", 1)
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.preload) > 0 && len(b.preload[len(b.preload)-1]) == 2
	})
	e := s.Engine.(*testEngine)
	e.mu.Lock()
	e.status.Buffered = e.status.Position
	e.mu.Unlock()
	c.post(func() { c.updatePreload() })
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.preload) > 0 && len(b.preload[len(b.preload)-1]) == 0
	})
}

func TestSeriesPreloadsTwoEpisodesAndPromotesWithoutSeekingAwayPreroll(t *testing.T) {
	b := &testBackend{}
	c, r := newControllerTest(t, b)
	c.Play("A", 1, 0, 1)
	waitSource(t, c, "A", 1)
	var warm *testEngine
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, window := range b.preload {
			if len(window) == 2 && window[0].Number == 2 && window[1].Number == 3 {
				return true
			}
		}
		return false
	})
	eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, e := range r.engines {
			e.mu.Lock()
			match := e.url == "A/2/1080p.m3u8" && !e.closed
			e.mu.Unlock()
			if match {
				warm = e
				return true
			}
		}
		return false
	})
	c.Command("nextEpisode", nil)
	s := waitSource(t, c, "A", 2)
	if s.Engine != warm {
		t.Fatal("next episode discarded its warmed decoder")
	}
	warm.mu.Lock()
	defer warm.mu.Unlock()
	if len(warm.seeks) != 0 {
		t.Fatalf("promotion invalidated preroll with seeks %v", warm.seeks)
	}
}

func TestBufferPressureStopsNativePollingBeforeCancelingBackgroundDownload(t *testing.T) {
	b := &testBackend{}
	c, r := newControllerTest(t, b)
	c.Play("A", 1, 0, 1)
	s := waitSource(t, c, "A", 1)
	var warm *testEngine
	eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, e := range r.engines {
			e.mu.Lock()
			match := e.url == "A/2/1080p.m3u8" && e.warmNetwork
			e.mu.Unlock()
			if match {
				warm = e
				return true
			}
		}
		return false
	})
	e := s.Engine.(*testEngine)
	e.mu.Lock()
	e.status.Buffering = true
	e.status.Buffered = e.status.Position
	e.mu.Unlock()
	eventually(t, func() bool {
		warm.mu.Lock()
		polling := warm.warmNetwork
		warm.mu.Unlock()
		b.mu.Lock()
		defer b.mu.Unlock()
		return !polling && len(b.preload) > 0 && len(b.preload[len(b.preload)-1]) == 0
	})
}

func TestFeedTailPreloadsNextEpisodeAndRetainsNegotiatedLowerQualityOnSwipe(t *testing.T) {
	b := &testBackend{feed: &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "A"}, {ID: "B"}, {ID: "C"}}}}
	b.resolve = func(_ context.Context, id string, n int, _ string) (*ipc.PlaybackDTO, error) {
		return source(id, n, "720p"), nil
	}
	c, r := newControllerTest(t, b)
	c.ShowFeed("")
	s := waitSource(t, c, "A", 1)
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, w := range b.preload {
			if len(w) > 0 && w[0].SeriesID == "B" {
				return true
			}
		}
		return false
	})
	s.Engine.Seek(195)
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, w := range b.preload {
			if len(w) == 2 && w[0].SeriesID == "A" && w[0].Number == 2 && w[1].SeriesID == "B" {
				return true
			}
		}
		return false
	})
	var warm avkit.Player
	c.post(func() {
		for _, candidate := range c.slots {
			if candidate.seriesID == "A" && candidate.number == 2 {
				warm = candidate.engine
			}
		}
	})
	barrier(t, c)
	if warm == nil {
		t.Fatal("feed automatic continuation has no warmed decoder")
	}
	e := s.Engine.(*testEngine)
	e.mu.Lock()
	e.status.Position = e.status.Duration
	e.status.Ended = true
	e.mu.Unlock()
	s = waitSource(t, c, "A", 2)
	if s.Engine != warm {
		t.Fatal("feed automatic continuation discarded its warmed decoder")
	}
	c.Command("next", nil)
	waitSource(t, c, "B", 1)
	b.mu.Lock()
	count := 0
	for _, req := range b.requests {
		if req.SeriesID == "B" && req.EpisodeNum == 1 {
			count++
		}
	}
	b.mu.Unlock()
	if count != 1 {
		t.Fatalf("swipe re-resolved an already negotiated 720p rendition %d times", count)
	}
	if open, audible := r.active(); open > 3 || audible > 1 {
		t.Fatal(fmt.Sprintf("decoder budget exceeded: open=%d audible=%d", open, audible))
	}
}
