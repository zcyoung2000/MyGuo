package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in macOS test of the production HTTP handler, controller and AVKit.
// Build cmd/native-player-qa and set MYGUO_NATIVE_PLAYER_QA to its path. A
// read-only local HLS directory can be supplied as MYGUO_NATIVE_HLS_FIXTURE;
// otherwise ffmpeg generates a local fixture. No account or network is used.
func TestNativeHLSStartupRunway(t *testing.T) {
	qa := os.Getenv("MYGUO_NATIVE_PLAYER_QA")
	if qa == "" {
		t.Skip("set MYGUO_NATIVE_PLAYER_QA for native AVKit startup acceptance")
	}
	fixture := os.Getenv("MYGUO_NATIVE_HLS_FIXTURE")
	if fixture == "" {
		ffmpeg, err := exec.LookPath("ffmpeg")
		if err != nil {
			t.Fatal(err)
		}
		fixture = t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error",
			"-f", "lavfi", "-i", "testsrc2=size=320x568:rate=24",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "44",
			"-c:v", "libx264", "-preset", "ultrafast", "-g", "96", "-sc_threshold", "0",
			"-c:a", "aac", "-f", "hls", "-hls_time", "4", "-hls_playlist_type", "event",
			"-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4",
			"-hls_segment_filename", filepath.Join(fixture, "seg%03d.m4s"), filepath.Join(fixture, "index.m3u8"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generate local HLS: %v: %s", err, out)
		}
	}
	original, err := os.ReadFile(filepath.Join(fixture, "index.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(original), "#EXTINF:")
	if count < 7 {
		t.Fatal("native fixture needs at least seven fragments")
	}
	ms, stream := startupTestServer(t, false)
	files, err := os.ReadDir(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Name() == "init.mp4" || strings.HasSuffix(file.Name(), ".m4s") {
			// Never write to the original cache or fixture.
			data, err := os.ReadFile(filepath.Join(fixture, file.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stream.dir, file.Name()), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	partial := func(n int) []byte {
		var lines []string
		segments := 0
		for _, line := range strings.Split(string(original), "\n") {
			if strings.HasPrefix(line, "#EXTINF:") {
				if segments == n {
					break
				}
				segments++
			}
			if line == "#EXT-X-ENDLIST" && n < count {
				break
			}
			lines = append(lines, line)
		}
		return []byte(strings.Join(lines, "\n") + "\n")
	}
	publish := func(n int) error {
		if err := os.WriteFile(stream.playlistPath+".tmp", partial(n), 0600); err != nil {
			return err
		}
		return os.Rename(stream.playlistPath+".tmp", stream.playlistPath)
	}
	if err := publish(1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var once sync.Once
	var publisher sync.WaitGroup
	publishErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			publisher.Add(1)
			go func() {
				defer publisher.Done()
				ticker := time.NewTicker(180 * time.Millisecond)
				defer ticker.Stop()
				for n := 2; n <= count; n++ {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
					if err := publish(n); err != nil {
						publishErrors <- err
						return
					}
				}
				stream.finish(nil)
			}()
		})
		if strings.HasSuffix(r.URL.Path, "/complete.m3u8") {
			writeHLSResponse(w, http.StatusOK, original)
			return
		}
		ms.handleHLS(w, r)
	}))
	defer func() { cancel(); server.Close(); publisher.Wait() }()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "native-player.json"), []byte(`{"version":1,"rate":2,"volume":0,"muted":true,"quality":"1080p","fit":"contain"}`), 0600); err != nil {
		t.Fatal(err)
	}
	base := server.URL + "/hls/710000000000001/1080p/"
	cmd := exec.CommandContext(ctx, qa, "-data", dataDir, "-source", base+"index.m3u8",
		"-fallback-source", base+"complete.m3u8", "-trace", "-quit-after", "12s")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native startup: %v\n%s", err, output)
	}
	select {
	case err := <-publishErrors:
		t.Fatalf("publish fixture: %v", err)
	default:
	}
	text := string(output)
	for _, failure := range []string{"restart buffered startup", "resume buffered stall", "recovering stalled stream", "fallback", "FAILED"} {
		// first-frame logs include a harmless fallback=0 counter.
		if strings.Contains(strings.ReplaceAll(text, "fallback=0", ""), failure) {
			t.Fatalf("startup needed recovery (%s):\n%s", failure, text)
		}
	}
	match := regexp.MustCompile(`clock-start episode=.* elapsedMs=(\d+) position=([\d.]+)`).FindStringSubmatch(text)
	if len(match) != 3 {
		t.Fatalf("native playback clock never started:\n%s", text)
	}
	msToPlay, _ := strconv.Atoi(match[1])
	startPosition, _ := strconv.ParseFloat(match[2], 64)
	if startPosition > 1 {
		t.Fatalf("a new episode started at the live edge (%.2fs), not its beginning:\n%s", startPosition, text)
	}
	if msToPlay >= 2000 {
		t.Fatalf("native clock took %dms despite a local startup runway:\n%s", msToPlay, text)
	}
	clocks := regexp.MustCompile(`\[qa-clock\].* position=([\d.]+).*buffering=(\w+)`).FindAllStringSubmatch(text, -1)
	previous := 0.0
	for _, clock := range clocks {
		position, _ := strconv.ParseFloat(clock[1], 64)
		if previous > 1 && (position-previous < .5 || clock[2] != "false") {
			t.Fatalf("startup delay became an early playback stall:\n%s", text)
		}
		previous = position
	}
	if previous < 10 {
		t.Fatalf("native playback did not continue at 2x: last position %.2f\n%s", previous, text)
	}
	t.Logf("AVKit 2x startup %dms; continued to %.2fs with no reopen or stall", msToPlay, previous)
}
