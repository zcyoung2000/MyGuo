package media

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startupTestPlaylist(segments int, complete bool) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-MAP:URI=\"init.mp4\"\n")
	for i := range segments {
		fmt.Fprintf(&b, "#EXTINF:3.750000,\nseg%03d.m4s\n", i)
	}
	if complete {
		b.WriteString("#EXT-X-ENDLIST\n")
	}
	return b.String()
}

func TestHLSPlaylistStartupRunway(t *testing.T) {
	for _, tc := range []struct {
		name     string
		segments int
		complete bool
		ready    bool
	}{
		{"first fragment", 1, false, false},
		{"only the live edge reserve", 3, false, false},
		{"insufficient runway at double speed", 6, false, false},
		{"growing runway", 7, false, true},
		{"completed short clip", 1, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hlsPlaylistStartupReady([]byte(startupTestPlaylist(tc.segments, tc.complete))); got != tc.ready {
				t.Fatalf("startup ready = %v, want %v", got, tc.ready)
			}
		})
	}
	if hlsPlaylistStartupReady([]byte("#EXTM3U\n#EXTINF:4,\nseg000.m4s\n")) {
		t.Fatal("a manifest without a target duration cannot establish a startup runway")
	}
}

func startupTestServer(t *testing.T, complete bool) (*MediaServer, *hlsStream) {
	t.Helper()
	ms := &MediaServer{cacheDir: t.TempDir()}
	dir := ms.hlsCacheDir("710000000000001", "1080p")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"init.mp4", "seg000.m4s", "seg001.m4s", "seg002.m4s", "seg003.m4s", "seg004.m4s", "seg005.m4s", "seg006.m4s"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("local fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	stream := &hlsStream{
		key: PrefetchTaskKey(PrefetchTask{EpisodeID: "710000000000001", Quality: "1080p"}),
		dir: dir, playlistPath: filepath.Join(dir, "index.m3u8"), done: make(chan struct{}),
	}
	if err := os.WriteFile(stream.playlistPath, []byte(startupTestPlaylist(1, complete)), 0600); err != nil {
		t.Fatal(err)
	}
	if complete {
		stream.finish(nil)
	}
	ms.hlsStreams = map[string]*hlsStream{stream.key: stream}
	return ms, stream
}

func serveStartupTestPlaylist(ms *MediaServer, ctx context.Context) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/hls/710000000000001/1080p/index.m3u8", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	ms.serveHLSPlaylist(response, request, "series", "710000000000001", 1, "1080p")
	return response
}

func TestHLSFirstResponseIncludesNewlyAvailableRunway(t *testing.T) {
	ms, stream := startupTestServer(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- serveStartupTestPlaylist(ms, ctx) }()
	select {
	case <-done:
		t.Fatal("the first response published only one fragment")
	case <-time.After(150 * time.Millisecond):
	}
	// Match ffmpeg's atomic playlist publication: no partially written file.
	if err := os.WriteFile(stream.playlistPath+".tmp", []byte(startupTestPlaylist(7, false)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(stream.playlistPath+".tmp", stream.playlistPath); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-done:
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "seg006.m4s") || !strings.Contains(response.Body.String(), "TIME-OFFSET=0") {
			t.Fatalf("startup response = %d %s", response.Code, response.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("startup waited after a playable runway was available")
	}
}

func TestHLSSlowProducerHasOneBoundedStartupWait(t *testing.T) {
	ms, _ := startupTestServer(t, false)
	start := time.Now()
	response := serveStartupTestPlaylist(ms, context.Background())
	if elapsed := time.Since(start); elapsed < hlsStartupCoalesceWindow || elapsed > hlsStartupCoalesceWindow+time.Second {
		t.Fatalf("slow startup wait = %v", elapsed)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "seg000.m4s") {
		t.Fatalf("slow source must still be playable: %d %s", response.Code, response.Body.String())
	}
	start = time.Now()
	serveStartupTestPlaylist(ms, context.Background())
	if elapsed := time.Since(start); elapsed >= hlsStartupCoalesceWindow {
		t.Fatalf("playlist refresh repeated the startup delay: %v", elapsed)
	}
}

func TestHLSCompletedShortClipDoesNotWaitForStartupRunway(t *testing.T) {
	ms, _ := startupTestServer(t, true)
	start := time.Now()
	response := serveStartupTestPlaylist(ms, context.Background())
	if elapsed := time.Since(start); elapsed >= hlsStartupCoalesceWindow {
		t.Fatalf("completed cache was delayed: %v", elapsed)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("completed response = %d %s", response.Code, response.Body.String())
	}
}

func TestHLSStartupWaitHonorsCancellation(t *testing.T) {
	ms, _ := startupTestServer(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- serveStartupTestPlaylist(ms, ctx) }()
	cancel()
	select {
	case response := <-done:
		if response.Code == http.StatusOK {
			t.Fatal("canceled startup returned a successful playlist")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("startup waited for the coalescing deadline after cancellation")
	}
}
