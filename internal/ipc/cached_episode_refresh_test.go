package ipc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters"
	"myguo/internal/hgapi/adapters/memstore"
)

// A neighbor slot resolved before its full-file prefetch completed carries a
// stale HLS DTO. Once the MP4 lands on disk, RefreshCachedEpisode must upgrade
// it locally so promotion uses the complete file without blocking on metadata.
func TestRefreshCachedEpisodeUpgradesStaleHLSDTO(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("full-file prefetch dto refresh is Windows-only")
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ms, err := NewMediaServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	const series, episode, quality = "710000000000001", "710000000000101", "1080p"

	// Write a complete-file cache entry at the path PlayEpisode will probe.
	path := episodeCachePath(ms.CacheDir(), episode, quality)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("local media fixture", 128)), 0600); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected metadata request", http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := adapters.NewClient("", hgapi.Options{
		ProfileStore:   memstore.New(),
		APIOrigin:      server.URL,
		RegisterOrigin: server.URL,
		RetryAttempts:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := &HongguoService{store: store, media: ms, client: client}
	stale := &PlaybackDTO{
		SeriesID:     series,
		EpisodeID:    episode,
		EpisodeNum:   1,
		Quality:      quality,
		StreamURL:    "http://127.0.0.1/hls.m3u8",
		HLSStreamURL: "http://127.0.0.1/hls.m3u8",
		IsFullCached: false,
	}

	got, err := svc.RefreshCachedEpisode(context.Background(), stale)
	if err != nil {
		t.Fatal(err)
	}
	if got == stale || !got.IsFullCached {
		t.Fatalf("stale HLS dto was not upgraded to a complete local file: %+v", got)
	}
	if got.StreamURL != got.FullStreamURL || got.FullStreamURL == "" || got.ProgressiveStreamURL != "" {
		t.Fatalf("upgraded dto must point StreamURL at the full file: stream=%q full=%q", got.StreamURL, got.FullStreamURL)
	}
	// applySlot's preroll path prefers HLS whenever HLSStreamURL is non-empty,
	// so the upgrade must clear it or promotion reloads the HLS cold start.
	if got.HLSStreamURL != "" {
		t.Fatalf("upgraded dto still carries a stale HLS url %q; preroll promotion would replay the HLS cold path", got.HLSStreamURL)
	}
	if requests.Load() != 0 {
		t.Fatalf("cache refresh made %d upstream requests; expected local-only DTO refresh", requests.Load())
	}
}

func TestRefreshCachedEpisodeLeavesUncachedAndAlreadyCachedDTOsAlone(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("full-file prefetch dto refresh is Windows-only")
	}
	svc := &HongguoService{media: &MediaServer{}}

	uncached := &PlaybackDTO{EpisodeID: "710000000000101", IsFullCached: false}
	if got, err := svc.RefreshCachedEpisode(context.Background(), uncached); err != nil || got != uncached {
		t.Fatalf("uncached dto should be returned untouched: got=%+v err=%v", got, err)
	}

	cached := &PlaybackDTO{EpisodeID: "710000000000101", IsFullCached: true}
	if got, err := svc.RefreshCachedEpisode(context.Background(), cached); err != nil || got != cached {
		t.Fatalf("already-cached dto should be returned untouched: got=%+v err=%v", got, err)
	}

	if got, err := svc.RefreshCachedEpisode(context.Background(), nil); err != nil || got != nil {
		t.Fatalf("nil dto should pass through: got=%+v err=%v", got, err)
	}
}
