package playback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSurfaceClickHidesThenPausesAndResumesWithoutReload(t *testing.T) {
	b := &testBackend{}
	c, r := newControllerTest(t, b)
	c.Play("A", 1, 27, 1)
	waitSource(t, c, "A", 1)
	c.Command("surface", nil)
	barrier(t, c)
	s := c.Snapshot()
	if !s.Preferences.Clear || s.Paused {
		t.Fatalf("first click interrupted playback: clear=%v paused=%v", s.Preferences.Clear, s.Paused)
	}
	c.Command("surface", nil)
	barrier(t, c)
	s = c.Snapshot()
	if s.Preferences.Clear || !s.Paused || s.Status.Playing {
		t.Fatal("second click did not pause with controls visible")
	}
	c.Command("surface", nil)
	barrier(t, c)
	s = c.Snapshot()
	if !s.Preferences.Clear || s.Paused {
		t.Fatal("paused picture did not resume clean playback")
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
		t.Fatalf("tap reloaded media or lost pause progress: requests=%d saved=%d", requests, saved)
	}
	r.mu.Lock()
	engine := r.engines[0]
	r.mu.Unlock()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if !engine.status.Playing || engine.status.Position != 27 {
		t.Fatalf("native engine not resumed at same position: %+v", engine.status)
	}
}

func TestFeedSurfaceClickPausesWithoutHidingControls(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.ShowFeed("推荐")
	waitSource(t, c, "A", 1)

	c.Command("surface", nil)
	barrier(t, c)
	s := c.Snapshot()
	if !s.Paused || s.Status.Playing || s.Preferences.Clear {
		t.Fatalf("feed click did not pause with controls visible: %+v", s)
	}

	c.Command("surface", nil)
	barrier(t, c)
	s = c.Snapshot()
	if !s.Paused || s.Preferences.Clear {
		t.Fatalf("second feed click changed the paused state: %+v", s)
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
