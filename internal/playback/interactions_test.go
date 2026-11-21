package playback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSeriesSurfaceClickOnlyTogglesChromeAndPlaybackButtonTogglesVideo(t *testing.T) {
	b := &testBackend{}
	c, r := newControllerTest(t, b)
	c.Play("A", 1, 27, 1)
	waitSource(t, c, "A", 1)
	c.Command("clear", true)
	barrier(t, c)
	c.Command("surface", nil)
	barrier(t, c)
	s := c.Snapshot()
	if s.Preferences.Clear || s.Paused || !s.Status.Playing {
		t.Fatalf("picture click changed playback instead of showing controls: %+v", s)
	}
	c.Command("surface", nil)
	barrier(t, c)
	s = c.Snapshot()
	if !s.Preferences.Clear || s.Paused || !s.Status.Playing {
		t.Fatalf("picture click changed playback instead of hiding controls: %+v", s)
	}
	c.Command("surface", nil)
	barrier(t, c)
	s = c.Snapshot()
	if s.Preferences.Clear || s.Paused || !s.Status.Playing {
		t.Fatalf("picture click did not show controls without pausing: %+v", s)
	}
	c.Command("toggle", nil)
	barrier(t, c)
	s = c.Snapshot()
	if !s.Paused || s.Status.Playing || s.Preferences.Clear {
		t.Fatal("explicit play/pause action did not pause while showing controls")
	}
	c.Command("surface", nil)
	barrier(t, c)
	s = c.Snapshot()
	if !s.Paused || !s.Preferences.Clear {
		t.Fatal("picture click resumed paused playback instead of only hiding controls")
	}
	c.Command("surface", nil)
	barrier(t, c)
	c.Command("toggle", nil)
	eventually(t, func() bool {
		s := c.Snapshot()
		return !s.Paused && s.Status.Playing
	})
	s = c.Snapshot()
	if s.Paused || !s.Status.Playing || s.Preferences.Clear {
		t.Fatalf("explicit play action did not resume playback: paused=%v playing=%v hidden=%v loading=%v error=%q", s.Paused, s.Status.Playing, s.Preferences.Clear, s.Loading, s.Error)
	}
	b.mu.Lock()
	requests := 0
	for _, request := range b.requests {
		if request.SeriesID == "A" && request.EpisodeNum == 1 {
			requests++
		}
	}
	saved := len(b.saved)
	b.mu.Unlock()
	if requests != 1 || saved == 0 {
		t.Fatalf("tap reloaded media or lost progress: requests=%d saved=%d", requests, saved)
	}
	r.mu.Lock()
	engine := r.engines[0]
	r.mu.Unlock()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if !engine.status.Playing || engine.status.Position != 27 {
		t.Fatalf("native engine did not continue at the same position: %+v", engine.status)
	}
}

func TestSeriesChromeHidesAfterTwoIdleSecondsAndActivityRestartsTimer(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.Play("A", 1, 0, 1)
	waitSource(t, c, "A", 1)

	activity := time.Now()
	c.post(func() {
		c.state.Preferences.Clear = false
		c.chromeActivity = activity
		if c.hideIdleChrome(activity.Add(playerChromeIdleTimeout - time.Millisecond)) {
			t.Error("controls hid before the two-second idle deadline")
		}
		if !c.hideIdleChrome(activity.Add(playerChromeIdleTimeout + time.Millisecond)) {
			t.Error("controls stayed visible after the two-second idle deadline")
		}
	})
	barrier(t, c)
	if !c.Snapshot().Preferences.Clear {
		t.Fatal("idle controls were not hidden")
	}

	c.Command("surface", nil)
	barrier(t, c)
	c.Command("activity", nil)
	barrier(t, c)
	activity = time.Now()
	c.post(func() {
		c.chromeActivity = activity
		c.state.Preferences.Clear = false
		if c.hideIdleChrome(activity.Add(playerChromeIdleTimeout - time.Millisecond)) {
			t.Error("recent activity did not restart the idle deadline")
		}
		c.state.Panel = "episodes"
		c.state.Preferences.Clear = false
		if c.hideIdleChrome(activity.Add(playerChromeIdleTimeout + time.Millisecond)) {
			t.Error("open panel was hidden by the chrome timer")
		}
		c.state.Panel = ""
		if !c.hideIdleChrome(activity.Add(playerChromeIdleTimeout + time.Millisecond)) {
			t.Error("closing an idle panel did not allow the controls to hide")
		}
	})
	barrier(t, c)
	if !c.Snapshot().Preferences.Clear {
		t.Fatal("controls did not hide after activity and the panel ended")
	}
}

func TestFeedSurfaceClickTogglesPlaybackWithoutHidingControlsOrReloading(t *testing.T) {
	b := &testBackend{}
	c, r := newControllerTest(t, b)
	c.ShowFeed("推荐")
	waitSource(t, c, "A", 1)

	c.Command("surface", nil)
	barrier(t, c)
	s := c.Snapshot()
	if !s.Paused || s.Status.Playing || s.Preferences.Clear {
		t.Fatalf("feed click did not pause with controls visible: %+v", s)
	}

	c.Command("surface", nil)
	eventually(t, func() bool { return c.Snapshot().Status.Playing })
	s = c.Snapshot()
	if s.Paused || s.Preferences.Clear {
		t.Fatalf("second feed click did not resume with controls visible: %+v", s)
	}
	c.Command("surface", nil)
	c.Command("surface", nil)
	barrier(t, c)
	if c.Snapshot().Paused || c.Snapshot().Preferences.Clear {
		t.Fatal("rapid feed clicks did not toggle the latest playback state")
	}
	r.mu.Lock()
	engine := r.engines[0]
	r.mu.Unlock()
	engine.mu.Lock()
	playing, loads := engine.status.Playing, len(engine.loads)
	engine.mu.Unlock()
	if !playing || loads != 1 {
		t.Fatalf("feed click reloaded or failed to resume the engine: playing=%v loads=%d", playing, loads)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	requests := 0
	for _, request := range b.requests {
		if request.SeriesID == "A" && request.EpisodeNum == 1 {
			requests++
		}
	}
	if requests != 1 {
		t.Fatalf("feed clicks renegotiated the current source %d times", requests)
	}
}

func TestSurfaceClickDismissesPanelBeforeChangingPlayback(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.Play("A", 1, 27, 1)
	waitSource(t, c, "A", 1)
	c.Command("panel", "episodes")
	c.Command("surface", nil)
	barrier(t, c)
	s := c.Snapshot()
	if s.Panel != "" || s.Preferences.Clear || s.Paused {
		t.Fatal("outside click changed playback instead of dismissing panel")
	}
	c.Command("pause", nil)
	c.Command("panel", "settings")
	c.Command("surface", nil)
	barrier(t, c)
	s = c.Snapshot()
	if s.Panel != "" || s.Preferences.Clear || !s.Paused {
		t.Fatal("dismissing panel resumed a paused video")
	}
}

func TestRapidSurfaceClicksUseTheLatestActorState(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.Play("A", 1, 0, 1)
	waitSource(t, c, "A", 1)
	c.Command("surface", nil)
	c.Command("surface", nil)
	c.Command("surface", nil)
	barrier(t, c)
	s := c.Snapshot()
	if s.Paused || !s.Preferences.Clear {
		t.Fatal("rapid clicks were interpreted using an old published snapshot")
	}
	c.Command("pause", nil)
	barrier(t, c)
	if c.Snapshot().Preferences.Clear {
		t.Fatal("keyboard/system pause left controls hidden")
	}
}

func TestCleanPlaybackDoesNotPersistAsAStartupPreference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "player.json")
	p := DefaultPreferences()
	p.Clear = true
	if err := SavePreferences(path, p); err != nil {
		t.Fatal(err)
	}
	if got := LoadPreferences(path); got.Clear || got.Quality != p.Quality {
		t.Fatal("transient visibility persisted or settings were lost")
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), `"clear"`) {
		t.Fatal("clear state written to disk")
	}
}
