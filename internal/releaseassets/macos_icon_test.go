package releaseassets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyReleaseChecksIconsInsideSignedMacOSUpdate(t *testing.T) {
	for _, scenario := range []string{"valid", "missing catalog", "changed catalog", "missing fallback", "changed fallback", "missing plist", "wrong catalog reference", "wrong fallback reference"} {
		t.Run(scenario, func(t *testing.T) {
			t.Chdir(t.TempDir())
			dir, config, key, m := signedFixture(t)
			config.Name = "MyGuo"
			config.MacOS.Icon = "resources/AppIconCatalog/Assets.car"
			config.MacOS.InfoPlist = map[string]string{"CFBundleIconFile": "MyGuo.icns"}
			for name, data := range map[string][]byte{
				config.MacOS.Icon:             []byte("compiled catalog"),
				"resources/darwin/MyGuo.icns": []byte("compiled icns"),
			} {
				if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			prefix := "MyGuo.app/Contents/"
			files := map[string][]byte{
				prefix + "Resources/Assets.car": []byte("compiled catalog"),
				prefix + "Resources/MyGuo.icns": []byte("compiled icns"),
				prefix + "Info.plist": []byte(`<plist><dict><key>CFBundleIconName</key><string>AppIcon</string>
<key>CFBundleIconFile</key><string>MyGuo.icns</string></dict></plist>`),
			}
			switch scenario {
			case "missing catalog":
				delete(files, prefix+"Resources/Assets.car")
			case "changed catalog":
				files[prefix+"Resources/Assets.car"] = []byte("another catalog")
			case "missing fallback":
				delete(files, prefix+"Resources/MyGuo.icns")
			case "changed fallback":
				files[prefix+"Resources/MyGuo.icns"] = []byte("PNG fallback")
			case "missing plist":
				delete(files, prefix+"Info.plist")
			case "wrong catalog reference":
				files[prefix+"Info.plist"] = bytes.ReplaceAll(files[prefix+"Info.plist"], []byte("AppIcon</string>"), []byte("OtherIcon</string>"))
			case "wrong fallback reference":
				files[prefix+"Info.plist"] = bytes.ReplaceAll(files[prefix+"Info.plist"], []byte("MyGuo.icns"), []byte("AppIcon.icns"))
			}
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			for name, data := range files {
				if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				if _, err := tw.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			archive := buf.Bytes()
			digest := sha256.Sum256(archive)
			m.Size = int64(len(archive))
			m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, digest[:]))
			writeFixture(t, dir, "myguo-0.1.6-darwin-arm64.tar.gz", archive)
			writeManifest(t, dir, m)
			err := Verify(dir, config, []string{"darwin-arm64"})
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "darwin-arm64:") {
				t.Fatalf("expected bundle icon verification error, got %v", err)
			}
		})
	}
}
