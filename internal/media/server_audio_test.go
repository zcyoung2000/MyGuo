package media

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFFmpegDownloadArgsMuxesSeparateAudio(t *testing.T) {
	args := ffmpegDownloadArgs("https://media.invalid/video", "https://media.invalid/audio", "0123456789abcdef0123456789abcdef", 12, false)
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"-decryption_key 0123456789abcdef0123456789abcdef -i https://media.invalid/video",
		"-i https://media.invalid/audio",
		"-map 0:v:0 -map 1:a:0",
		"-t 12",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if strings.Count(joined, "-decryption_key") != 2 {
		t.Fatalf("separate audio input was not decrypted: %q", joined)
	}
}

func TestFFmpegDownloadArgsKeepsSingleTrack(t *testing.T) {
	args := ffmpegDownloadArgs("https://media.invalid/video", "", "", 0, false)
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "-map") {
		t.Fatalf("single-track args unexpectedly remapped streams: %q", joined)
	}
	if strings.Contains(joined, "-t") {
		t.Fatalf("full download unexpectedly time-limited: %q", joined)
	}
}

func TestFFmpegDownloadArgsTranscodesHEVCToFullResolutionH264(t *testing.T) {
	joined := strings.Join(ffmpegDownloadArgs("https://media.invalid/video", "", "", 0, true), " ")
	for _, required := range []string{"-hwaccel auto", "-pix_fmt yuv420p", "-c:v h264_mf", "-profile:v high", "-b:v 5M", "-g 60", "-c:a copy", "-f mp4"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("transcode args missing %q: %s", required, joined)
		}
	}
	if strings.Index(joined, "-hwaccel auto") > strings.Index(joined, "-i https://media.invalid/video") {
		t.Fatalf("hardware acceleration must be selected before the video input: %s", joined)
	}
	if strings.Contains(joined, "scale=") {
		t.Fatalf("transcode args unexpectedly reduce resolution: %s", joined)
	}
}

func TestEpisodeCachePathInvalidatesV1AudiolessFiles(t *testing.T) {
	path := episodeCachePath("/tmp", "710000000000001", DefaultVideoQuality)
	if want := filepath.Join("/tmp", "episodes", "710000000000001_v2.mp4"); path != want {
		t.Fatalf("cache path = %q, want %q", path, want)
	}
}
