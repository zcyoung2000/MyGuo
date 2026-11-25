//go:build ignore

// Build the Windows x64 installer on Windows, macOS or Linux:
//
//	go run ./scripts/build-windows.go
//
// FFmpeg is pinned to an immutable release archive and checked before use.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const ffmpegVersion = "9.0.2"
const ffmpegArchiveSHA = "60f467265b1e312373dbcd92200c2618a74850f98d3d078e94296bb3fa2047ba"
const ffmpegURL = "https://github.com/GyanD/codexffmpeg/releases/download/9.0.2/ffmpeg-9.0.2-essentials_build.zip"
const ffmpegSourceSHA = "8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e"

func main() {
	archive := flag.String("ffmpeg-archive", "", "use an already downloaded FFmpeg archive (checksum verified)")
	minimal := flag.String("ffmpeg-dir", "", "use a minimal FFmpeg directory with its verified build manifest")
	flag.Parse()
	if err := build(*archive, *minimal); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(archive, minimal string) error {
	var config struct{ Slug, Version, Out string }
	data, err := os.ReadFile("mygo.json")
	if err != nil {
		return fmt.Errorf("run this command from the repository root: %w", err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	if config.Out == "" {
		config.Out = "dist"
	}
	if config.Slug == "" || config.Version == "" {
		return fmt.Errorf("mygo.json needs slug and version")
	}
	if archive != "" && minimal != "" {
		return fmt.Errorf("choose either -ffmpeg-archive or -ffmpeg-dir")
	}
	if archive == "" && minimal == "" {
		if _, err := os.Stat(filepath.Join("resources", "windows-amd64", "ffmpeg-build-info.json")); err == nil {
			minimal = filepath.Join("resources", "windows-amd64")
		}
	}
	if minimal != "" {
		if err := installMinimalFFmpeg(minimal); err != nil {
			return err
		}
	} else {
		if archive == "" {
			cache, err := os.UserCacheDir()
			if err != nil {
				return err
			}
			archive = filepath.Join(cache, "myguo-build", "ffmpeg-"+ffmpegVersion+"-windows.zip")
			if checksum(archive) != ffmpegArchiveSHA {
				if err := download(archive); err != nil {
					return err
				}
			}
		}
		if checksum(archive) != ffmpegArchiveSHA {
			return fmt.Errorf("FFmpeg archive checksum mismatch")
		}
		if err := extractFFmpeg(archive); err != nil {
			return err
		}
	}

	cmd := exec.Command("go", "run", "github.com/egoist/mygo/cmd/mygo", "build", "-platform", "windows/amd64")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	output := filepath.Join(config.Out, "windows-amd64", config.Slug+"-setup-"+config.Version+"-amd64.exe")
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		return fmt.Errorf("Windows installer was not generated; install NSIS (makensis) and rebuild")
	}
	fmt.Printf("\nWindows installer: %s\nSHA256: %s\n", output, checksum(output))
	return nil
}

func installMinimalFFmpeg(directory string) error {
	var info struct {
		Format, Version, Target, SourceSHA256 string
		Files                                 map[string]string
	}
	data, err := os.ReadFile(filepath.Join(directory, "ffmpeg-build-info.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return err
	}
	if info.Format != "myguo-ffmpeg-minimal-v1" || info.Version != ffmpegVersion || info.Target != "windows/amd64" || info.SourceSHA256 != ffmpegSourceSHA {
		return fmt.Errorf("minimal FFmpeg manifest has an unsupported source or target")
	}
	names := []string{"ffmpeg.exe", "ffmpeg-LICENSE.txt", "ffmpeg-README.txt"}
	for _, name := range names {
		if want := info.Files[name]; len(want) != 64 || checksum(filepath.Join(directory, name)) != want {
			return fmt.Errorf("minimal FFmpeg checksum mismatch: %s", name)
		}
	}
	destination := filepath.Join("resources", "windows-amd64")
	if err := os.MkdirAll(destination, 0755); err != nil {
		return err
	}
	for _, name := range append(names, "ffmpeg-build-info.json") {
		source := filepath.Join(directory, name)
		target := filepath.Join(destination, name)
		sourceInfo, err := os.Stat(source)
		if err != nil {
			return err
		}
		if targetInfo, err := os.Stat(target); err == nil && os.SameFile(sourceInfo, targetInfo) {
			continue
		}
		if err := copyResource(source, target); err != nil {
			return err
		}
	}
	fmt.Printf("Using verified minimal Windows FFmpeg %s\n", info.Version)
	return nil
}

func copyResource(source, destination string) error {
	reader, err := os.Open(source)
	if err != nil {
		return err
	}
	defer reader.Close()
	writer, err := os.CreateTemp(filepath.Dir(destination), "ffmpeg-resource-*")
	if err != nil {
		return err
	}
	defer writer.Close()
	defer os.Remove(writer.Name())
	if _, err := io.Copy(writer, reader); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return os.Rename(writer.Name(), destination)
}

func checksum(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func download(destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(destination), "ffmpeg-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	fmt.Printf("Downloading Windows FFmpeg %s…\n", ffmpegVersion)
	client := &http.Client{Timeout: 15 * time.Minute}
	response, err := client.Get(ffmpegURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("FFmpeg download: HTTP %d", response.StatusCode)
	}
	if _, err := io.Copy(f, io.LimitReader(response.Body, 200<<20)); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if checksum(f.Name()) != ffmpegArchiveSHA {
		return fmt.Errorf("downloaded FFmpeg checksum mismatch")
	}
	return os.Rename(f.Name(), destination)
}

func extractFFmpeg(archive string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	destination := filepath.Join("resources", "windows-amd64")
	if err := os.MkdirAll(destination, 0755); err != nil {
		return err
	}
	files := map[string]string{
		"bin/ffmpeg.exe": "ffmpeg.exe",
		"LICENSE":        "ffmpeg-LICENSE.txt",
		"README.txt":     "ffmpeg-README.txt",
	}
	for suffix, name := range files {
		var source *zip.File
		for _, entry := range z.File {
			if strings.HasSuffix(entry.Name, "/"+suffix) {
				source = entry
				break
			}
		}
		if source == nil {
			return fmt.Errorf("FFmpeg archive missing %s", suffix)
		}
		reader, err := source.Open()
		if err != nil {
			return err
		}
		writer, err := os.Create(filepath.Join(destination, name))
		if err != nil {
			reader.Close()
			return err
		}
		_, copyErr := io.Copy(writer, reader)
		reader.Close()
		closeErr := writer.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err := os.Remove(filepath.Join(destination, "ffmpeg-build-info.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
