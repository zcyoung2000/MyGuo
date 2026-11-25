package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters"
	"myguo/internal/hgapi/adapters/memstore"
)

func TestProtectedFullCacheDownloadUsesContentKeyAndDecrypts(t *testing.T) {
	const playbackPath = hgapi.PlaybackPath
	const registerPath = "/service/2/device_register/"
	const sourceURL = "https://protected.invalid/720.mp4"
	videoModel := map[string]any{
		"status":     10,
		"video_id":   "protected-video",
		"media_type": "video",
		"video_list": []map[string]any{{
			"main_url": sourceURL,
			"encrypt_info": map[string]any{
				"encrypt":           true,
				"encryption_method": "cenc-aes-ctr",
				"kid":               "test-kid",
				"spade_a":           "nLwSxG6JI85zhT//c7AK4HauO+lzrg7TdZwO0XWrP+J0pDypqQ==",
			},
			"video_meta": map[string]any{
				"definition": "720p",
				"codec_type": "bytevc1",
				"vwidth":     720,
				"vheight":    1280,
				"bitrate":    1_000_000,
				"size":       1_000_000,
			},
		}},
	}
	encodedModel, err := json.Marshal(videoModel)
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case registerPath:
			_, _ = w.Write([]byte(`{"new_user":1,"device_id_str":"7100000000000001","install_id_str":"7100000000000002"}`))
		case playbackPath:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    0,
				"message": "ok",
				"data":    map[string]any{"7100000000000201": map[string]any{"video_model": string(encodedModel)}},
			})
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	profilePath := filepath.Join(t.TempDir(), "profile.json")
	client, err := adapters.NewClient(profilePath, hgapi.Options{
		ProfileStore:   memstore.New(),
		RegisterOrigin: server.URL,
		APIOrigin:      server.URL,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	pb, err := client.ResolvePlayback(context.Background(), "7100000000000201", hgapi.PlaybackOptions{})
	if !errors.Is(err, hgapi.ErrDRMProtected) || pb == nil {
		t.Fatalf("ResolvePlayback() error = %v, playback = %+v", err, pb)
	}
	key, err := hgapi.ContentKey(pb)
	if err != nil {
		t.Fatalf("ContentKey() error = %v", err)
	}
	script := fmt.Sprintf(`#!/bin/bash
key=
url=
previous=
for argument; do
  if [ "$previous" = "-decryption_key" ]; then key="$argument"; fi
  if [ "$previous" = "-i" ]; then url="$argument"; fi
  previous="$argument"
done
if [ "$key" != "%s" ] || [ "$url" != "%s" ]; then
  exit 97
fi
dd if=/dev/zero of="${@: -1}" bs=1024 count=2 status=none
`, key.Hex, sourceURL)
	var ffmpegPath string
	if runtime.GOOS == "windows" {
		// A .bat file is not a native executable and cannot be passed directly
		// to exec.CommandContext on Windows. Build a tiny native helper instead,
		// so this test exercises the same process-launch path as real ffmpeg.
		dir := t.TempDir()
		sourcePath := filepath.Join(dir, "ffmpeg_stub.go")
		ffmpegPath = filepath.Join(dir, "ffmpeg_stub.exe")
		const helper = `package main

import (
	"os"
)

func main() {
	args := os.Args[1:]
	key, url := "", ""
	for i, arg := range args {
		if i > 0 && args[i-1] == "-decryption_key" {
			key = arg
		}
		if i > 0 && args[i-1] == "-i" {
			url = arg
		}
	}
	if key != os.Getenv("MYGO_TEST_FFMPEG_KEY") || url != os.Getenv("MYGO_TEST_FFMPEG_URL") || len(args) == 0 {
		os.Exit(97)
	}
	if err := os.WriteFile(args[len(args)-1], make([]byte, 2048), 0600); err != nil {
		os.Exit(98)
	}
}
`
		if err := os.WriteFile(sourcePath, []byte(helper), 0600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("go", "build", "-o", ffmpegPath, sourcePath).CombinedOutput(); err != nil {
			t.Fatalf("build ffmpeg test helper: %v\n%s", err, output)
		}
		t.Setenv("MYGO_TEST_FFMPEG_KEY", key.Hex)
		t.Setenv("MYGO_TEST_FFMPEG_URL", sourceURL)
	} else {
		ffmpegPath = filepath.Join(t.TempDir(), "ffmpeg")
		if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HONGGUO_FFMPEG", ffmpegPath)

	ms := &MediaServer{
		client:   client,
		cacheDir: t.TempDir(),
		pbCache:  make(map[string]playbackCacheItem),
		pbLoads:  make(map[string]*playbackLoad),
	}
	destPath := episodeCachePath(ms.cacheDir, "7100000000000201", "720p")
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := ms.downloadAndDecrypt(context.Background(), "7100000000000201", "720p", destPath); err != nil {
		t.Fatalf("downloadWithQualityFallback() error = %v", err)
	}
	info, err := os.Stat(destPath)
	if err != nil || info.Size() != 2048 {
		t.Fatalf("decrypted cache stat = %v, err = %v", info, err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(destPath)); len(entries) != 1 {
		t.Fatalf("temporary files were not cleaned: %v", entries)
	}
	if !FileExists(destPath) {
		t.Fatal("decrypted file is not considered playable")
	}
}
