package media

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"myguo/internal/hgapi/model"
)

func TestEpisodeCachePathSeparatesQualities(t *testing.T) {
	defaultWant := filepath.Join("/tmp", "episodes", "710000000000001_v2.mp4")
	if got := episodeCachePath("/tmp", "710000000000001", DefaultVideoQuality); got != defaultWant {
		t.Fatalf("default path = %q", got)
	}
	hdWant := filepath.Join("/tmp", "episodes", "710000000000001_1080p_v2.mp4")
	if got := episodeCachePath("/tmp", "710000000000001", "1080p"); got != hdWant {
		t.Fatalf("1080p path = %q", got)
	}
}

func TestProgressiveVideoURLRequestsStreamingMode(t *testing.T) {
	ms := &MediaServer{port: 12345}
	got := ms.ProgressiveVideoURL("ep1", "series1", 1, "720p")
	if !strings.Contains(got, "http://127.0.0.1:12345/video?") ||
		!strings.Contains(got, "mode=progressive") ||
		!strings.Contains(got, "quality=720p") {
		t.Fatalf("progressive URL = %q", got)
	}
}

func TestMediaRoutesRequireLocalHostAndRandomToken(t *testing.T) {
	ms, err := newMediaServer(nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	handler := ms.server.Handler
	serve := func(host, target string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Host = host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	localHost := fmt.Sprintf("127.0.0.1:%d", ms.port)
	if got := serve(localHost, "/img"); got.Code != http.StatusForbidden {
		t.Fatalf("image request without token status = %d", got.Code)
	}
	if got := serve("attacker.invalid", "/img?token="+ms.token); got.Code != http.StatusForbidden {
		t.Fatalf("request with foreign Host status = %d", got.Code)
	}
	if got := serve(localHost, "/img?token="+ms.token); got.Code != http.StatusOK || got.Header().Get("X-Image-Fallback") != "1" {
		t.Fatalf("authorized local request = %d, fallback=%q", got.Code, got.Header().Get("X-Image-Fallback"))
	}
	if got := serve(localHost, "/health"); got.Code != http.StatusOK || got.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("health response = %d, ACAO=%q", got.Code, got.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestSafePublicDialRejectsLoopback(t *testing.T) {
	if conn, err := safePublicDialContext(context.Background(), "tcp", "127.0.0.1:80"); err == nil {
		_ = conn.Close()
		t.Fatal("loopback destination was allowed")
	}
}

func TestIsPublicIPRejectsNonRoutableRanges(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "100.127.255.254", "169.254.1.1", "::1", "fc00::1", "::ffff:100.64.0.1"} {
		if isPublicIP(net.ParseIP(value)) {
			t.Errorf("isPublicIP(%q) = true, want false", value)
		}
	}
	for _, value := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !isPublicIP(net.ParseIP(value)) {
			t.Errorf("isPublicIP(%q) = false, want true", value)
		}
	}
	if isPublicIP(nil) {
		t.Fatal("isPublicIP(nil) = true, want false")
	}
}

func TestValidatePublicURLSkipsLocalDNSWhenUsingProxy(t *testing.T) {
	target := &url.URL{Scheme: "https", Host: "unresolvable.invalid"}
	proxyURL := &url.URL{Scheme: "http", Host: "proxy.internal:8080"}
	err := validatePublicURLWithProxy(context.Background(), target, func(req *http.Request) (*url.URL, error) {
		if req.URL != target {
			t.Fatalf("proxy lookup URL = %v, want %v", req.URL, target)
		}
		return proxyURL, nil
	})
	if err != nil {
		t.Fatalf("validatePublicURLWithProxy() error = %v", err)
	}
}

func TestQualitySelectionFromPlayback(t *testing.T) {
	pb := &model.Playback{Streams: []model.Stream{
		{Definition: "360p", URL: "https://media.invalid/360"},
		{Definition: "1080p", URL: "https://media.invalid/1080"},
	}}

	selection := qualitySelectionFromPlayback(pb, "720p")
	if selection.Selected != "1080p" {
		t.Fatalf("nearest selection = %q, want 1080p", selection.Selected)
	}

	active := 0
	for _, opt := range selection.Options {
		if opt.ID != "720p" && opt.ID != "1080p" && opt.ID != "360p" && opt.Available {
			t.Fatalf("unexpected quality %q marked available", opt.ID)
		}
		if opt.Active {
			active++
			if opt.ID != "1080p" {
				t.Fatalf("active option = %q, want 1080p", opt.ID)
			}
		}
	}
	if active != 1 {
		t.Fatalf("active options = %d", active)
	}
}

func TestQualitySelectionCarriesStreamDiagnostics(t *testing.T) {
	pb := &model.Playback{Streams: []model.Stream{{
		Definition: "720p",
		URL:        "https://media.invalid/720",
		Codec:      "h264",
		Width:      1280,
		Height:     720,
		Bitrate:    1_500_000,
	}}}

	selection := qualitySelectionFromPlayback(pb, "720p")
	option := selection.Options[1]
	if !option.Available || !option.Active {
		t.Fatalf("unexpected option state: %+v", option)
	}
	if option.Codec != "h264" || option.Width != 1280 || option.Height != 720 || option.Bitrate != 1_500_000 {
		t.Fatalf("stream diagnostics were not propagated: %+v", option)
	}
}

func TestWindowsQualitySelectionPrefersH264Streams(t *testing.T) {
	pb := &model.Playback{Streams: []model.Stream{
		{Definition: "1080p", URL: "https://media.invalid/1080-hevc", Codec: "hvc1.2.4.L153.B0", Bitrate: 8_000_000},
		{Definition: "1080p", URL: "https://media.invalid/1080-h264", Codec: "avc1.640028", Bitrate: 5_000_000},
		{Definition: "720p", URL: "https://media.invalid/720-h264", Codec: "h264", Bitrate: 2_500_000},
	}}

	selection := qualitySelectionFromPlaybackWithH264Preference(pb, "1080p", true)
	if selection.Selected != "1080p" {
		t.Fatalf("selected quality = %q, want 1080p", selection.Selected)
	}
	if option := selection.Options[0]; !option.Available || option.Codec != "avc1.640028" {
		t.Fatalf("1080p option = %+v, want the H.264 rendition", option)
	}

	stream, err := selectVideoStreamWithH264Preference(pb, selection.Selected, true)
	if err != nil {
		t.Fatal(err)
	}
	if stream.URL != "https://media.invalid/1080-h264" {
		t.Fatalf("selected stream = %q, want H.264 rendition", stream.URL)
	}
}

func TestWindowsQualitySelectionKeepsHEVCWhenDecoderIsAvailable(t *testing.T) {
	pb := &model.Playback{Streams: []model.Stream{
		{Definition: "1080p", URL: "https://media.invalid/1080-hevc", Codec: "hvc1.2.4.L153.B0", Bitrate: 8_000_000},
		{Definition: "720p", URL: "https://media.invalid/720-h264", Codec: "avc1.640028", Bitrate: 2_500_000},
	}}

	selection := qualitySelectionFromPlaybackWithH264Preference(pb, "1080p", false)
	if selection.Options[0].Codec != "hvc1.2.4.L153.B0" {
		t.Fatalf("1080p option = %+v, want original HEVC stream", selection.Options[0])
	}
	if selection.Selected != "1080p" {
		t.Fatalf("selected quality = %q, want 1080p when HEVC decoding is available", selection.Selected)
	}
	stream, err := selectVideoStreamWithH264Preference(pb, "1080p", false)
	if err != nil {
		t.Fatal(err)
	}
	if stream.URL != "https://media.invalid/1080-hevc" {
		t.Fatalf("selected stream = %q, want HEVC rendition", stream.URL)
	}
}

func TestWindowsQualitySelectionDowngradesToAvailableH264(t *testing.T) {
	pb := &model.Playback{Streams: []model.Stream{
		{Definition: "1080p", URL: "https://media.invalid/1080-hevc", Codec: "hevc", Bitrate: 8_000_000},
		{Definition: "720p", URL: "https://media.invalid/720-h264", Codec: "h264", Bitrate: 2_500_000},
	}}

	selection := qualitySelectionFromPlaybackWithH264Preference(pb, "1080p", true)
	if selection.Selected != "720p" {
		t.Fatalf("selected quality = %q, want compatible 720p", selection.Selected)
	}
	if selection.Options[0].Available || !selection.Options[1].Available || selection.Options[1].Codec != "h264" {
		t.Fatalf("quality options expose incompatible streams: %+v", selection.Options)
	}
	stream, err := selectVideoStreamWithH264Preference(pb, selection.Selected, true)
	if err != nil {
		t.Fatal(err)
	}
	if stream.URL != "https://media.invalid/720-h264" {
		t.Fatalf("selected stream = %q, want 720p H.264 rendition", stream.URL)
	}
}

func TestWindowsQualitySelectionKeepsExistingBehaviorWithoutH264(t *testing.T) {
	pb := &model.Playback{Streams: []model.Stream{
		{Definition: "1080p", URL: "https://media.invalid/1080-hevc", Codec: "hvc1", Bitrate: 8_000_000},
		{Definition: "720p", URL: "https://media.invalid/720-hevc", Codec: "hevc", Bitrate: 3_000_000},
	}}

	selection := qualitySelectionFromPlaybackWithH264Preference(pb, "1080p", true)
	if selection.Selected != "1080p" || selection.Options[0].Codec != "hvc1" {
		t.Fatalf("selection without H.264 changed: %+v", selection)
	}
	stream, err := selectVideoStreamWithH264Preference(pb, selection.Selected, true)
	if err != nil {
		t.Fatal(err)
	}
	if stream.URL != "https://media.invalid/1080-hevc" {
		t.Fatalf("fallback stream = %q, want existing HEVC selection", stream.URL)
	}
}

func TestWindowsPlaybackUsesSeparateCodecCompatibleCache(t *testing.T) {
	const episodeID = "710000000000001"
	legacy := episodeCachePathForPlatform("/tmp", episodeID, DefaultVideoQuality, false)
	windows := episodeCachePathForPlatform("/tmp", episodeID, DefaultVideoQuality, true)
	if legacy == windows || !strings.Contains(windows, "windows_compat_transcoded") {
		t.Fatalf("Windows cache = %q, legacy cache = %q", windows, legacy)
	}
	legacyHLS := hlsCacheDirForPlatform("/tmp", episodeID, DefaultVideoQuality, false)
	windowsHLS := hlsCacheDirForPlatform("/tmp", episodeID, DefaultVideoQuality, true)
	if legacyHLS == windowsHLS || !strings.Contains(windowsHLS, "windows_compat_transcoded") {
		t.Fatalf("Windows HLS cache = %q, legacy cache = %q", windowsHLS, legacyHLS)
	}
}

func TestWindowsH264PreferenceRequiresMissingHEVCDecoder(t *testing.T) {
	for _, tt := range []struct {
		name    string
		windows bool
		hevc    bool
		want    bool
	}{
		{name: "Windows without HEVC decoder", windows: true, hevc: false, want: true},
		{name: "Windows with HEVC decoder", windows: true, hevc: true, want: false},
		{name: "other platform without HEVC decoder", windows: false, hevc: false, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUseWindowsH264Preference(tt.windows, tt.hevc); got != tt.want {
				t.Fatalf("shouldUseWindowsH264Preference(%t, %t) = %t, want %t", tt.windows, tt.hevc, got, tt.want)
			}
		})
	}
}

func TestWindowsHEVCTranscodeOnlyWhenSystemDecoderMissing(t *testing.T) {
	for _, tt := range []struct {
		name    string
		codec   string
		windows bool
		hevc    bool
		want    bool
	}{
		{name: "HEVC without system decoder", codec: "hvc1.2.4.L153.B0", windows: true, want: true},
		{name: "HEVC extension installed", codec: "hev1.1.6.L120.B0", windows: true, hevc: true},
		{name: "H.264 stream", codec: "avc1.640028", windows: true, want: false},
		{name: "other platform", codec: "hevc", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldTranscodeHEVCForWindows(tt.codec, tt.windows, tt.hevc); got != tt.want {
				t.Fatalf("shouldTranscodeHEVCForWindows(%q, %t, %t) = %t, want %t", tt.codec, tt.windows, tt.hevc, got, tt.want)
			}
		})
	}
}

func TestHEVCCompatibilityDimensionsFitMediaFoundationFallback(t *testing.T) {
	for _, tt := range []struct {
		width, height int
		wantWidth     int
		wantHeight    int
	}{
		{width: 1920, height: 1080, wantWidth: 720, wantHeight: 406},
		{width: 1080, height: 1920, wantWidth: 324, wantHeight: 576},
		{width: 640, height: 480, wantWidth: 640, wantHeight: 480},
	} {
		width, height := hevcCompatibilityDimensions(tt.width, tt.height)
		if width != tt.wantWidth || height != tt.wantHeight {
			t.Fatalf("hevcCompatibilityDimensions(%d, %d) = %d×%d, want %d×%d", tt.width, tt.height, width, height, tt.wantWidth, tt.wantHeight)
		}
	}
}

func TestSelectVideoStreamUsesNearestDefinition(t *testing.T) {
	pb := &model.Playback{Streams: []model.Stream{
		{Definition: "360p", URL: "https://media.invalid/360"},
		{Definition: "1080p", URL: "https://media.invalid/1080"},
	}}

	stream, err := selectVideoStream(pb, "480p")
	if err != nil {
		t.Fatalf("selectVideoStream err = %v", err)
	}
	if stream.Definition != "360p" {
		t.Fatalf("selected definition = %q", stream.Definition)
	}
}

func TestSelectVideoStreamNilPlaybackReturnsError(t *testing.T) {
	_, err := selectVideoStream(nil, "720p")
	if err == nil {
		t.Fatal("selectVideoStream(nil) should return error, got nil")
	}
	if !strings.Contains(err.Error(), "playback is unavailable") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestVideoQualityFallbackOrderPrefersLowerQualities(t *testing.T) {
	got := videoQualityFallbackOrder("720p")
	want := []string{"720p", "540p", "480p", "360p", "1080p"}
	if len(got) != len(want) {
		t.Fatalf("fallback order length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fallback order = %v, want %v", got, want)
		}
	}
}

func TestRemoveLegacyPreviewsAndWarmups(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "ep1_preview_v2.mp4")
	warmup := filepath.Join(dir, "ep1_warm_v2.mp4")
	for _, path := range []string{legacy, warmup} {
		if err := os.WriteFile(path, []byte("media"), 0644); err != nil {
			t.Fatalf("write %s failed: %v", path, err)
		}
	}

	removeLegacyPreviews(dir)
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy preview still exists (err=%v)", err)
	}
	if _, err := os.Stat(warmup); !os.IsNotExist(err) {
		t.Fatalf("legacy warmup still exists (err=%v)", err)
	}
}

func TestPrefetchEpisodeQueuesTask(t *testing.T) {
	ms := &MediaServer{
		cacheDir:       t.TempDir(),
		prefetchQ:      make(chan PrefetchTask, 10),
		prefetchQueued: make(map[string]PrefetchTask),
		prefetchActive: make(map[string]*PrefetchExecution),
	}

	ms.PrefetchEpisode("series1", "ep1", 1, "720p")
	select {
	case task := <-ms.prefetchQ:
		if task.SeriesID != "series1" || task.EpisodeID != "ep1" || task.Number != 1 || task.Quality != "720p" {
			t.Fatalf("unexpected task: %+v", task)
		}
	default:
		t.Fatal("expected prefetch task in queue")
	}
}

func TestServeEpisodeFileSupportsRangeRequests(t *testing.T) {
	dir := t.TempDir()
	mediaPath := filepath.Join(dir, "episode_v2.mp4")
	data := make([]byte, 2048)
	if err := os.WriteFile(mediaPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	ms := &MediaServer{cacheDir: dir}
	request := httptest.NewRequest("GET", "/video", nil)
	request.Header.Set("Range", "bytes=0-99")
	response := httptest.NewRecorder()

	ms.serveEpisodeFile(response, request, mediaPath)
	if response.Code != http.StatusPartialContent {
		t.Fatalf("range response code = %d, want %d", response.Code, http.StatusPartialContent)
	}
	if got := response.Header().Get("Content-Range"); got != "bytes 0-99/2048" {
		t.Fatalf("content range = %q", got)
	}
	if response.Body.Len() != 100 {
		t.Fatalf("range body length = %d, want 100", response.Body.Len())
	}
}

func TestEvictDirLRU(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "old.mp4")
	file2 := filepath.Join(dir, "mid.mp4")
	file3 := filepath.Join(dir, "new.mp4")
	data := make([]byte, 100)

	for _, file := range []string{file1, file2, file3} {
		if err := os.WriteFile(file, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-10 * time.Minute)
	midTime := time.Now().Add(-5 * time.Minute)
	newTime := time.Now()
	_ = os.Chtimes(file1, oldTime, oldTime)
	_ = os.Chtimes(file2, midTime, midTime)
	_ = os.Chtimes(file3, newTime, newTime)

	evictDirLRU(dir, 250, 150, nil)
	if _, err := os.Stat(file1); !os.IsNotExist(err) {
		t.Errorf("expected file1 to be evicted, err: %v", err)
	}
	if _, err := os.Stat(file2); !os.IsNotExist(err) {
		t.Errorf("expected file2 to be evicted, err: %v", err)
	}
	if _, err := os.Stat(file3); err != nil {
		t.Errorf("expected file3 to remain, err: %v", err)
	}
}

func TestEvictDirLRUSkipsPinnedFile(t *testing.T) {
	dir := t.TempDir()
	pinned := filepath.Join(dir, "pinned.mp4")
	evictable := filepath.Join(dir, "evictable.mp4")
	data := make([]byte, 100)
	if err := os.WriteFile(pinned, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evictable, data, 0644); err != nil {
		t.Fatal(err)
	}

	evictDirLRU(dir, 150, 50, map[string]struct{}{pinned: {}})
	if _, err := os.Stat(pinned); err != nil {
		t.Fatalf("expected pinned file to remain: %v", err)
	}
	if _, err := os.Stat(evictable); !os.IsNotExist(err) {
		t.Fatalf("expected evictable file to be removed, err: %v", err)
	}
}

func TestPrefetchEpisodeDeduplicatesAndReplacesWindow(t *testing.T) {
	ms := &MediaServer{
		cacheDir:       t.TempDir(),
		prefetchQ:      make(chan PrefetchTask, 8),
		prefetchQueued: make(map[string]PrefetchTask),
		prefetchActive: make(map[string]*PrefetchExecution),
	}

	ms.PrefetchEpisode("series1", "ep1", 1, "720p")
	ms.PrefetchEpisode("series1", "ep1", 1, "720p")
	if len(ms.prefetchQ) != 1 {
		t.Fatalf("expected one queued task, got %d", len(ms.prefetchQ))
	}

	ms.ReplacePrefetchWindow([]PrefetchTask{
		{SeriesID: "series2", EpisodeID: "ep2", Number: 1, Quality: "720p"},
	})
	if len(ms.prefetchQ) != 1 {
		t.Fatalf("expected replacement to leave one task, got %d", len(ms.prefetchQ))
	}
	task := <-ms.prefetchQ
	if task.EpisodeID != "ep2" || task.SeriesID != "series2" {
		t.Fatalf("unexpected replacement task: %+v", task)
	}
}

func TestReplacePrefetchWindowKeepsPriorityOrder(t *testing.T) {
	ms := &MediaServer{
		cacheDir:       t.TempDir(),
		prefetchQ:      make(chan PrefetchTask, 4),
		prefetchQueued: make(map[string]PrefetchTask),
		prefetchActive: make(map[string]*PrefetchExecution),
	}

	ms.ReplacePrefetchWindow([]PrefetchTask{
		{SeriesID: "series3", EpisodeID: "ep3", Quality: "720p", Priority: 3},
		{SeriesID: "series1", EpisodeID: "ep1", Quality: "720p", Priority: 1},
		{SeriesID: "series2", EpisodeID: "ep2", Quality: "720p", Priority: 2},
	})

	want := []string{"ep1", "ep2", "ep3"}
	for _, episodeID := range want {
		select {
		case task := <-ms.prefetchQ:
			if task.EpisodeID != episodeID {
				t.Fatalf("task order = %s, want %s", task.EpisodeID, episodeID)
			}
		default:
			t.Fatalf("expected task %s in queue", episodeID)
		}
	}
}

func TestMediaNetworkSnapshotProfilesAndRecentFailures(t *testing.T) {
	ms := &MediaServer{
		networkBytesPerSec: 2 * 1024 * 1024,
		networkSamples:     3,
	}
	if profile, _, _ := ms.mediaNetworkSnapshot(); profile != "strong" {
		t.Fatalf("profile = %q, want strong", profile)
	}

	ms.networkErrors = 1
	ms.networkLastErrorAt = time.Now()
	if profile, _, failures := ms.mediaNetworkSnapshot(); profile != "medium" || failures != 1 {
		t.Fatalf("profile = %q, failures = %d, want medium and 1", profile, failures)
	}
}

func TestReplacePrefetchWindowProtectionTasksDoNotQueueDownloads(t *testing.T) {
	ms := &MediaServer{
		cacheDir:       t.TempDir(),
		prefetchQ:      make(chan PrefetchTask, 4),
		prefetchQueued: make(map[string]PrefetchTask),
		prefetchActive: make(map[string]*PrefetchExecution),
	}

	ms.ReplacePrefetchWindow([]PrefetchTask{
		{SeriesID: "series1", EpisodeID: "current", Number: 1, Quality: "720p", ProtectOnly: true},
		{SeriesID: "series2", EpisodeID: "far-next", Number: 1, Quality: "720p", PrefetchOnly: true},
	})
	if len(ms.prefetchQ) != 0 {
		t.Fatalf("protection-only task queued background work: %+v", <-ms.prefetchQ)
	}
}

func TestRunPrefetchTaskPrefetchOnlyDoesNotDownload(t *testing.T) {
	ms := &MediaServer{}
	ms.runPrefetchTask(context.Background(), PrefetchTask{
		SeriesID:     "series1",
		EpisodeID:    "episode1",
		Number:       1,
		Quality:      "720p",
		PrefetchOnly: true,
	}, "prefetch-only")
	if ms.fullDownloads.Load() != 0 {
		t.Fatalf("prefetch-only task started a full download: n=%d", ms.fullDownloads.Load())
	}
}

func TestReplacePrefetchWindowCancelsStaleActiveWork(t *testing.T) {
	canceled := make(chan struct{})
	ms := &MediaServer{
		prefetchQ:      make(chan PrefetchTask, 4),
		prefetchQueued: make(map[string]PrefetchTask),
		prefetchActive: map[string]*PrefetchExecution{
			"episode\x00stale\x00720p": {
				cancel: func() {
					select {
					case <-canceled:
					default:
						close(canceled)
					}
				},
			},
		},
	}

	ms.ReplacePrefetchWindow([]PrefetchTask{
		{SeriesID: "series1", EpisodeID: "current", Quality: "720p", ProtectOnly: true},
	})
	select {
	case <-canceled:
	default:
		t.Fatal("stale active prefetch was not canceled")
	}
}

func TestEpisodeCacheCallbackAccessIsSynchronized(t *testing.T) {
	ms := &MediaServer{}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			ms.SetEpisodeCacheCallback(func(string, string, int, string) error { return nil })
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			ms.rememberCachedEpisode("series", "episode", 1, "720p")
		}
	}()
	wg.Wait()
}

func TestReplacePrefetchWindowPreservesNewestRendererStream(t *testing.T) {
	oldCanceled := make(chan struct{})
	oldStream := &progressiveStream{
		key:           "episode\x00old\x00720p",
		startedAt:     time.Now().Add(-time.Minute),
		rendererOwned: true,
		cancel: func() {
			select {
			case <-oldCanceled:
			default:
				close(oldCanceled)
			}
		},
	}
	nextDone := make(chan struct{})
	close(nextDone)
	nextStream := &hlsStream{
		key:           "episode\x00next\x00720p",
		startedAt:     time.Now(),
		rendererOwned: true,
		cancel:        func() {},
		done:          nextDone,
	}
	ms := &MediaServer{
		prefetchQ:          make(chan PrefetchTask, 4),
		prefetchQueued:     make(map[string]PrefetchTask),
		prefetchActive:     make(map[string]*PrefetchExecution),
		progressiveStreams: map[string]*progressiveStream{oldStream.key: oldStream},
		hlsStreams:         map[string]*hlsStream{nextStream.key: nextStream},
	}

	ms.ReplacePrefetchWindow([]PrefetchTask{
		{SeriesID: "series-current", EpisodeID: "current", Quality: "720p", ProtectOnly: true},
	})
	select {
	case <-oldCanceled:
	default:
		t.Fatal("old renderer stream was not canceled")
	}
	ms.hlsMu.Lock()
	_, retained := ms.hlsStreams[nextStream.key]
	ms.hlsMu.Unlock()
	if !retained {
		t.Fatal("newest renderer-owned HLS stream was not retained")
	}
}

func TestReplacePrefetchWindowPreservesActiveRendererReader(t *testing.T) {
	canceled := make(chan struct{})
	activeStream := &progressiveStream{
		key:           "episode\x00active\x00720p",
		startedAt:     time.Now().Add(-time.Minute),
		rendererOwned: true,
		readers:       2,
		cancel: func() {
			select {
			case <-canceled:
			default:
				close(canceled)
			}
		},
	}
	newestDone := make(chan struct{})
	close(newestDone)
	newestStream := &hlsStream{
		key:           "episode\x00newest\x00540p",
		startedAt:     time.Now(),
		rendererOwned: true,
		cancel:        func() {},
		done:          newestDone,
	}
	ms := &MediaServer{
		prefetchQ:          make(chan PrefetchTask, 4),
		prefetchQueued:     make(map[string]PrefetchTask),
		prefetchActive:     make(map[string]*PrefetchExecution),
		progressiveStreams: map[string]*progressiveStream{activeStream.key: activeStream},
		hlsStreams:         map[string]*hlsStream{newestStream.key: newestStream},
	}

	ms.ReplacePrefetchWindow([]PrefetchTask{
		{SeriesID: "series-current", EpisodeID: "current", Quality: "540p", ProtectOnly: true},
	})

	select {
	case <-canceled:
		t.Fatal("active renderer stream was canceled")
	default:
	}
	ms.progressiveMu.Lock()
	_, retained := ms.progressiveStreams[activeStream.key]
	ms.progressiveMu.Unlock()
	if !retained {
		t.Fatal("active renderer stream was removed")
	}
}

func TestReplacePrefetchWindowPreservesRecentlyRequestedHLSStream(t *testing.T) {
	canceled := make(chan struct{})
	activeDone := make(chan struct{})
	close(activeDone)
	activeStream := &hlsStream{
		key:                "episode\x00active-hls\x00720p",
		startedAt:          time.Now().Add(-time.Minute),
		rendererOwned:      true,
		cancel:             func() { close(canceled) },
		done:               activeDone,
		lastMediaRequestAt: time.Now(),
	}
	newestStream := &progressiveStream{
		key:           "episode\x00newest-progressive\x00540p",
		startedAt:     time.Now(),
		rendererOwned: true,
		cancel:        func() {},
	}
	ms := &MediaServer{
		prefetchQ:          make(chan PrefetchTask, 4),
		prefetchQueued:     make(map[string]PrefetchTask),
		prefetchActive:     make(map[string]*PrefetchExecution),
		progressiveStreams: map[string]*progressiveStream{newestStream.key: newestStream},
		hlsStreams:         map[string]*hlsStream{activeStream.key: activeStream},
	}

	ms.ReplacePrefetchWindow([]PrefetchTask{
		{SeriesID: "series-current", EpisodeID: "current", Quality: "540p", ProtectOnly: true},
	})

	select {
	case <-canceled:
		t.Fatal("recently requested HLS stream was canceled")
	default:
	}
	ms.hlsMu.Lock()
	_, retained := ms.hlsStreams[activeStream.key]
	ms.hlsMu.Unlock()
	if !retained {
		t.Fatal("recently requested HLS stream was removed")
	}
}
