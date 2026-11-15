package playback

import (
	"testing"
	"time"

	"myguo/internal/avkit"
	"myguo/internal/ipc"
)

func recoveryFixture() (*Controller, *slot, *testEngine, avkit.Status) {
	e := &testEngine{}
	_ = e.Load("hls")
	s := &slot{engine: e, first: true, sourceURL: "hls", resume: 137, dto: &ipc.PlaybackDTO{EpisodeID: "episode", HLSStreamURL: "hls", FullStreamURL: "full"}}
	status := avkit.Status{Ready: true, Buffering: true, Position: 40, Buffered: 40, Duration: 200}
	c := &Controller{current: s, state: State{Status: status, Preferences: Preferences{Rate: 2}}}
	return c, s, e, status
}

func TestStalledHLSSwitchesToFullSourceAtCurrentClock(t *testing.T) {
	c, s, e, status := recoveryFixture()
	now := time.Now()
	c.recoverStall(s, status, now)
	if c.recoverStall(s, status, now.Add(7*time.Second)) {
		t.Fatal("ordinary buffering switched too early")
	}
	if !c.recoverStall(s, status, now.Add(8*time.Second)) || e.url != "full" || len(e.seeks) != 1 || e.seeks[0] != 40 || !c.state.Loading {
		t.Fatalf("stalled stream did not recover at current clock: url=%s seeks=%v loading=%t", e.url, e.seeks, c.state.Loading)
	}
	// Closing while the new asset is still loading must save the retained
	// clock, rather than replacing history with the new decoder's initial 0.
	e.status.Ready, e.status.Position = false, 0
	c.saveCurrent()
	if len(c.progressQueue) != 1 || c.progressQueue[0].position != 40 {
		t.Fatal("pending fallback seek lost progress during source loading")
	}
	s.seekPending = false
	// Exhaustion must offer retry instead of spinning forever or reloading.
	s.first = true
	c.recoverStall(s, status, now.Add(9*time.Second))
	c.recoverStall(s, status, now.Add(18*time.Second))
	if c.state.Error == "" || len(e.loads) != 2 {
		t.Fatal("exhausted recovery did not stop with a useful error")
	}
}

func TestBufferedStallResumesOnceAndProgressResetsWatchdog(t *testing.T) {
	c, s, e, status := recoveryFixture()
	status.Buffered = 60
	now := time.Now()
	c.recoverStall(s, status, now)
	c.recoverStall(s, status, now.Add(3*time.Second))
	c.recoverStall(s, status, now.Add(4*time.Second))
	if e.plays != 1 || e.url != "hls" {
		t.Fatal("buffered decoder was reloaded or repeatedly resumed")
	}
	status.Position = 41
	c.recoverStall(s, status, now.Add(7*time.Second))
	if c.recoverStall(s, status, now.Add(10*time.Second)) || e.url != "hls" {
		t.Fatal("advancing clock was treated as a persistent stall")
	}
}

func TestStallRecoveryLeavesPauseSeekAndEndIntact(t *testing.T) {
	for _, kind := range []string{"paused", "seeking", "ended", "starting"} {
		t.Run(kind, func(t *testing.T) {
			c, s, e, status := recoveryFixture()
			s.clockAdvanced = time.Now().Add(-time.Minute)
			switch kind {
			case "paused":
				c.state.Paused = true
			case "seeking":
				s.seekPending = true
			case "ended":
				status.Ended = true
			case "starting":
				s.first = false
			}
			if c.recoverStall(s, status, time.Now()) || len(e.loads) != 1 || e.plays != 0 {
				t.Fatal("intentional pause/seek/end/start triggered recovery")
			}
		})
	}
}

func TestPartialHLSEOFDoesNotAdvanceEpisode(t *testing.T) {
	b := &testBackend{}
	c, _ := newControllerTest(t, b)
	c.Play("A", 1, 0, 1)
	s := waitSource(t, c, "A", 1)
	e := s.Engine.(*testEngine)
	e.mu.Lock()
	e.status.Position, e.status.Duration, e.status.Ended, e.status.Playing = 31.9, 32, true, false
	e.mu.Unlock()
	eventually(t, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.url == "full:A"
	})
	if got := c.Snapshot(); got.Playback.EpisodeNum != 1 {
		t.Fatal("partial HLS EOF skipped the rest of the episode")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.seeks) == 0 || e.seeks[len(e.seeks)-1] != 31.9 {
		t.Fatalf("recovery lost playback position: %v", e.seeks)
	}
}
