package playback

import (
	"context"
	"testing"

	"myguo/internal/ipc"
)

// refreshBackend wraps testBackend with the optional refresher so the
// controller consults it on promotion without changing every other test.
type refreshBackend struct {
	*testBackend
	refreshed *ipc.PlaybackDTO
	calls     int
}

func (b *refreshBackend) RefreshCachedEpisode(_ context.Context, dto *ipc.PlaybackDTO) (*ipc.PlaybackDTO, error) {
	b.calls++
	if b.refreshed != nil {
		return b.refreshed, nil
	}
	return dto, nil
}

func waitPlayback(t *testing.T, c *Controller, num int) State {
	t.Helper()
	eventually(t, func() bool {
		s := c.Snapshot()
		return s.Playback != nil && s.Playback.EpisodeNum == num
	})
	return c.Snapshot()
}

func lastLoadedURL(r *engineRegistry) string {
	last := ""
	for _, e := range r.engines {
		if e.url != "" {
			last = e.url
		}
	}
	return last
}

// A neighbor slot resolved before its MP4 finished carries a stale HLS dto.
// On promotion applySlot must ask the refresher for an upgraded dto and load
// the local complete file, not the slow HLS stream.
func TestApplySlotUpgradesStaleDtoBeforeLoading(t *testing.T) {
	stale := source("series", 2, "1080p")
	stale.LastPosition = 0
	upgraded := *stale
	upgraded.IsFullCached = true
	upgraded.StreamURL = "full:series/local-complete.mp4"
	upgraded.FullStreamURL = upgraded.StreamURL
	upgraded.LastPosition = 0

	b := &refreshBackend{
		testBackend: &testBackend{resolve: func(_ context.Context, id string, num int, q string) (*ipc.PlaybackDTO, error) {
			return stale, nil
		}},
		refreshed: &upgraded,
	}
	c, r := newControllerTest(t, b.testBackend)
	c.backend = b

	c.selectSlot(ipc.SeriesCard{ID: "series"}, 2, -1, 1)
	s := waitPlayback(t, c, 2)
	if s.Playback != &upgraded && (!s.Playback.IsFullCached || s.Playback.StreamURL != upgraded.StreamURL) {
		t.Fatalf("promotion did not use refreshed dto: %+v", s.Playback)
	}
	if b.calls == 0 {
		t.Fatal("applySlot did not consult the cached-episode refresher")
	}
	if got := lastLoadedURL(r); got != upgraded.StreamURL {
		t.Fatalf("promotion loaded %q, want refreshed full-file URL %q", got, upgraded.StreamURL)
	}
}

// Backends without the optional refresher keep the original behavior.
func TestApplySlotWithoutRefresherLoadsOriginalDto(t *testing.T) {
	b := &testBackend{resolve: func(_ context.Context, id string, num int, q string) (*ipc.PlaybackDTO, error) {
		dto := source(id, num, q)
		dto.LastPosition = 0
		return dto, nil
	}}
	c, r := newControllerTest(t, b)
	c.selectSlot(ipc.SeriesCard{ID: "series"}, 1, -1, 1)
	waitPlayback(t, c, 1)

	// Without a refresher the stale dto is used as-is: nativeSourceURL picks
	// its HLS URL because IsFullCached is false. That is the original behavior.
	want := nativeSourceURL(source("series", 1, "1080p"))
	if got := lastLoadedURL(r); got != want {
		t.Fatalf("without refresher, promotion loaded %q, want original %q", got, want)
	}
}
