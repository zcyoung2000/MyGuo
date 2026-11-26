package media

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFeedPreloadPreviousGraceIsBoundedAndReservesCurrentWorker(t *testing.T) {
	ms := &MediaServer{cacheDir: t.TempDir(), hlsStreams: make(map[string]*hlsStream)}
	ids := []string{"710000000000001", "710000000000002", "710000000000003", "710000000000004"}
	counts := map[string]*atomic.Int32{}
	items := make([]FeedPreloadItem, 0, 4)
	for _, id := range ids {
		item := FeedPreloadItem{EpisodeID: id, Quality: "720p"}
		items = append(items, item)
		key := PrefetchTaskKey(PrefetchTask{EpisodeID: id, Quality: "720p"})
		dir := ms.hlsCacheDir(id, "720p")
		playlist := writeHLSFixture(t, dir, false)
		data, err := os.ReadFile(playlist)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(playlist, []byte(strings.ReplaceAll(string(data), "#EXTINF:2,", "#EXTINF:6,")), 0600); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		count := &atomic.Int32{}
		counts[id] = count
		ms.hlsStreams[key] = &hlsStream{key: key, dir: dir, playlistPath: playlist, done: done, lastMediaRequestAt: time.Now(), cancel: func() {
			if count.Add(1) == 1 {
				close(done)
			}
		}}
	}
	ms.SetFeedPreloadWindow(1, ids[0], "720p", items[1:3])
	ms.SetFeedPreloadWindow(2, ids[1], "720p", items[2:])
	if counts[ids[0]].Load() != 0 || counts[ids[1]].Load() != 0 || counts[ids[2]].Load() != 0 || counts[ids[3]].Load() != 1 {
		t.Fatal("previous + current + nearest must reserve all three workers, canceling only farthest")
	}
	ms.feedPreloadPreviousUntil = time.Now().Add(-time.Second)
	ms.SetFeedPreloadWindow(3, ids[1], "720p", items[2:3])
	if counts[ids[0]].Load() != 1 || counts[ids[1]].Load() != 0 {
		t.Fatal("expired previous stream was retained or current was canceled")
	}
	ms.feedPreloadPrevious = PrefetchTaskKey(PrefetchTask{EpisodeID: ids[2], Quality: "720p"})
	ms.feedPreloadPreviousUntil = time.Now().Add(time.Minute)
	ms.SetFeedPreloadWindow(4, ids[1], "720p", nil)
	if counts[ids[2]].Load() != 1 || counts[ids[1]].Load() != 0 {
		t.Fatal("playback pressure did not yield the previous writer to current")
	}
	ms.SetFeedPreloadWindow(5, "", "720p", nil)
}

func TestFeedPreloadStatusCountsOnlyPublishedPlayableSegments(t *testing.T) {
	ms := &MediaServer{cacheDir: t.TempDir()}
	task := PrefetchTask{EpisodeID: "710000000000001", Quality: "540p"}
	dir := ms.hlsCacheDir(task.EpisodeID, task.Quality)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	playlist := "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2.5,\nseg000.m4s\n#EXTINF:3.5,\nseg001.m4s\n"
	for name, data := range map[string]string{
		"index.m3u8": playlist, "init.mp4": "init", "seg000.m4s": "segment", "seg999.m4s.tmp": "unpublished",
	} {
		if err := writeTestFile(filepath.Join(dir, name), data); err != nil {
			t.Fatal(err)
		}
	}
	if status := ms.feedPreloadStatus(task); status.CachedSeconds != 0 || status.Complete {
		t.Fatalf("missing published segment was counted as ready: %+v", status)
	}
	if err := writeTestFile(filepath.Join(dir, "seg001.m4s"), "segment"); err != nil {
		t.Fatal(err)
	}
	status := ms.feedPreloadStatus(task)
	if status.CachedSeconds != 6 || status.Complete || status.Quality != "540p" {
		t.Fatalf("prefix readiness = %+v", status)
	}
	if err := appendHLSEndList(filepath.Join(dir, "index.m3u8")); err != nil {
		t.Fatal(err)
	}
	if status := ms.feedPreloadStatus(task); !status.Complete || status.CachedSeconds != 6 {
		t.Fatalf("completed readiness = %+v", status)
	}
}

func TestFeedPreloadWindowPromotionAndBackpressureKeepCurrentAndCompleteCache(t *testing.T) {
	ms := &MediaServer{cacheDir: t.TempDir(), hlsStreams: make(map[string]*hlsStream)}
	canceled := make(map[string]bool)
	items := []FeedPreloadItem{
		{EpisodeID: "710000000000002", Quality: "540p"},
		{EpisodeID: "710000000000003", Quality: "720p"},
	}
	for _, item := range append([]FeedPreloadItem{{EpisodeID: "710000000000001", Quality: "720p"}}, items...) {
		key := PrefetchTaskKey(PrefetchTask{EpisodeID: item.EpisodeID, Quality: item.Quality})
		dir := ms.hlsCacheDir(item.EpisodeID, item.Quality)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string]string{
			"index.m3u8": "#EXTM3U\n#EXTINF:6,\nseg000.m4s\n#EXT-X-ENDLIST\n",
			"seg000.m4s": strings.Repeat("x", 2048),
		} {
			if err := writeTestFile(filepath.Join(dir, name), data); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan struct{})
		close(done)
		ms.hlsStreams[key] = &hlsStream{key: key, dir: dir, playlistPath: filepath.Join(dir, "index.m3u8"),
			done: done, cancel: func() { canceled[key] = true }, rendererOwned: true, lastMediaRequestAt: time.Now()}
	}
	ms.SetFeedPreloadWindow(1, "710000000000001", "720p", items)
	ms.SetFeedPreloadWindow(2, items[0].EpisodeID, items[0].Quality, items[1:])
	ms.SetFeedPreloadWindow(3, items[0].EpisodeID, items[0].Quality, nil)
	currentKey := PrefetchTaskKey(PrefetchTask{EpisodeID: items[0].EpisodeID, Quality: items[0].Quality})
	if canceled[currentKey] || len(canceled) != 2 {
		t.Fatalf("backpressure/promotion canceled wrong streams: %v", canceled)
	}
	if !hlsPlaylistComplete(filepath.Join(ms.hlsCacheDir(items[1].EpisodeID, items[1].Quality), "index.m3u8")) {
		t.Fatal("shrinking a window deleted the completed cache")
	}
	ms.SetFeedPreloadWindow(2, "710000000000001", "720p", items)
	if canceled[currentKey] || ms.feedPreloadCurrent != currentKey || ms.hlsStarts.Load() != 0 {
		t.Fatal("stale window displaced the protected current video")
	}
}

func TestFeedPreloadWindowHLSAdmissionByPlatform(t *testing.T) {
	ms := &MediaServer{cacheDir: t.TempDir(), hlsStreams: make(map[string]*hlsStream)}
	neighbor := FeedPreloadItem{EpisodeID: "710000000000002", Quality: "720p"}
	// A cold neighbor with no pre-seeded stream: the HLS admission loop would
	// start a speculative stream on platforms that publish buffered ranges.
	statuses := ms.SetFeedPreloadWindow(1, "710000000000001", "720p", []FeedPreloadItem{neighbor})
	if len(statuses) != 1 {
		t.Fatalf("statuses = %d, want 1", len(statuses))
	}
	key := PrefetchTaskKey(PrefetchTask{EpisodeID: neighbor.EpisodeID, Quality: neighbor.Quality})
	if runtime.GOOS == "windows" {
		// Windows leaves neighbor lookahead to the full-file prefetch. No HLS
		// stream may register, or runPrefetchTask would skip the MP4 download.
		_, streamStarted := ms.hlsStreams[key]
		if streamStarted || ms.hlsStarts.Load() != 0 {
			t.Fatalf("Windows started a speculative HLS stream: registered=%t starts=%d", streamStarted, ms.hlsStarts.Load())
		}
		return
	}
	// A nil-client stream can fail and retire before this assertion. Count
	// admission instead of racing the worker's removal from hlsStreams.
	if ms.hlsStarts.Load() != 1 {
		t.Fatalf("macOS did not start the HLS preload: starts=%d", ms.hlsStarts.Load())
	}
}

func TestFeedPreloadWindowRejectsInvalidEpisodeIDsWithoutStartingWork(t *testing.T) {
	ms := &MediaServer{cacheDir: t.TempDir()}
	items := []FeedPreloadItem{{EpisodeID: "../../outside"}, {EpisodeID: ""}, {EpisodeID: "invalid"}}
	if statuses := ms.SetFeedPreloadWindow(1, "", "720p", items); len(statuses) != 0 || ms.hlsStarts.Load() != 0 {
		t.Fatalf("invalid preloads admitted: %+v", statuses)
	}
}

func TestFeedPreloadReorderedWindowProtectsNearestPrefix(t *testing.T) {
	for _, prefixReady := range []bool{false, true} {
		t.Run(map[bool]string{false: "nearest still cold", true: "nearest playable"}[prefixReady], func(t *testing.T) {
			ms := &MediaServer{cacheDir: t.TempDir(), hlsStreams: make(map[string]*hlsStream)}
			items := []FeedPreloadItem{
				{EpisodeID: "710000000000002", Quality: "720p"},
				{EpisodeID: "710000000000003", Quality: "720p"},
			}
			canceled := make(map[string]bool)
			for _, item := range items {
				key := PrefetchTaskKey(PrefetchTask{EpisodeID: item.EpisodeID, Quality: item.Quality})
				done := make(chan struct{})
				dir := ms.hlsCacheDir(item.EpisodeID, item.Quality)
				playlist := writeHLSFixture(t, dir, false)
				if prefixReady {
					data, err := os.ReadFile(playlist)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(playlist, []byte(strings.ReplaceAll(string(data), "#EXTINF:2,", "#EXTINF:6,")), 0600); err != nil {
						t.Fatal(err)
					}
				}
				ms.hlsStreams[key] = &hlsStream{key: key, dir: dir, playlistPath: playlist, done: done,
					cancel: func() { canceled[key] = true; close(done) }}
			}
			statuses := ms.SetFeedPreloadWindow(1, "710000000000001", "720p", items)
			nearKey := PrefetchTaskKey(PrefetchTask{EpisodeID: items[0].EpisodeID, Quality: "720p"})
			farKey := PrefetchTaskKey(PrefetchTask{EpisodeID: items[1].EpisodeID, Quality: "720p"})
			if len(statuses) != 2 || !statuses[0].Active || statuses[1].Active != prefixReady || canceled[nearKey] || canceled[farKey] == prefixReady {
				t.Fatalf("preload should protect cold prefixes and keep playable renditions running: %+v, canceled=%v", statuses, canceled)
			}
			if prefixReady {
				ms.SetFeedPreloadWindow(2, "710000000000001", "720p", items)
				if len(canceled) != 0 {
					t.Fatalf("a repeated window restarted warmed media: %v", canceled)
				}
			}
		})
	}
}
