package appdirs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAppDirsAdoptLegacyData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MYGUO_DATA_ROOT", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	for _, base := range []string{appSupportBase, appCacheBase} {
		legacyDir := platformAppDir(home, base, legacyAppName, runtime.GOOS, os.Getenv)
		if err := os.MkdirAll(legacyDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(legacyDir, "data"), []byte("legacy"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	appDir, err := AppSupportDir(home)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := AppCacheDir(home)

	for i, dir := range []string{appDir, cacheDir} {
		if got, err := os.ReadFile(filepath.Join(dir, "data")); err != nil || string(got) != "legacy" {
			t.Fatalf("legacy data was not adopted in %s: %q, %v", dir, got, err)
		}
		base := []string{appSupportBase, appCacheBase}[i]
		if _, err := os.Stat(platformAppDir(home, base, legacyAppName, runtime.GOOS, os.Getenv)); !os.IsNotExist(err) {
			t.Fatalf("legacy directory still exists beside %s: %v", dir, err)
		}
	}
}

func TestPlatformAppDirs(t *testing.T) {
	home, local := t.TempDir(), t.TempDir()
	for _, tt := range []struct {
		name, goos, base, local, want string
	}{
		{"mac data", "darwin", appSupportBase, "", filepath.Join(home, "Library", "Application Support", AppName)},
		{"mac cache", "darwin", appCacheBase, "", filepath.Join(home, "Library", "Caches", AppName)},
		{"Windows data", "windows", appSupportBase, local, filepath.Join(local, AppName)},
		{"Windows cache", "windows", appCacheBase, local, filepath.Join(local, AppName, "Cache")},
		{"Windows fallback", "windows", appSupportBase, "", filepath.Join(home, "AppData", "Local", AppName)},
		{"Windows relative env", "windows", appCacheBase, "relative", filepath.Join(home, "AppData", "Local", AppName, "Cache")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := platformAppDir(home, tt.base, AppName, tt.goos, func(key string) string {
				if key == "LOCALAPPDATA" {
					return tt.local
				}
				return ""
			})
			if got != tt.want {
				t.Fatalf("directory = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestDataRootOverridesPlatformAndIsIsolated(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MYGUO_DATA_ROOT", root)
	home := t.TempDir()
	data, err := AppSupportDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if data != filepath.Join(root, appSupportBase, AppName) || AppCacheDir(home) != filepath.Join(root, appCacheBase, AppName) {
		t.Fatal("test data root was not respected")
	}
	t.Setenv("MYGUO_DATA_ROOT", "relative")
	if _, err := AppSupportDir(home); err == nil {
		t.Fatal("relative override accepted")
	}
}
