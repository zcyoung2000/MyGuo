package appdirs

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

const (
	AppName         = "myguo"
	legacyAppName   = "hongguo-mygo"
	appSupportBase  = "Application Support"
	appCacheBase    = "Caches"
	libraryBasePath = "Library"
)

func AppSupportDir(home string) (string, error) {
	return migrateAppDir(home, appSupportBase)
}

func AppCacheDir(home string) string {
	dir, err := migrateAppDir(home, appCacheBase)
	if err != nil {
		log.Printf("[App] Warning: failed to prepare %s cache directory: %v", AppName, err)
		return platformAppDir(home, appCacheBase, AppName, runtime.GOOS, os.Getenv)
	}
	return dir
}

// migrateAppDir adopts data written by the previous app identity when the new
// directory does not exist yet, so users keep their profile and media caches.
func migrateAppDir(home, base string) (string, error) {
	// A separate data root lets native integration runs exercise real services
	// without touching the installed app's profile, progress or media cache.
	if root := os.Getenv("MYGUO_DATA_ROOT"); root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("MYGUO_DATA_ROOT must be an absolute path")
		}
		dir := filepath.Join(root, base, AppName)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
		return dir, nil
	}
	newDir := platformAppDir(home, base, AppName, runtime.GOOS, os.Getenv)
	legacyDir := platformAppDir(home, base, legacyAppName, runtime.GOOS, os.Getenv)

	if _, err := os.Stat(newDir); err == nil {
		return newDir, nil
	}
	if err := os.MkdirAll(filepath.Dir(newDir), 0755); err != nil {
		return "", err
	}
	if _, err := os.Stat(legacyDir); err == nil {
		if err := os.Rename(legacyDir, newDir); err != nil {
			log.Printf("[App] Warning: could not migrate %s to %s: %v", legacyDir, newDir, err)
		} else {
			log.Printf("[App] Migrated %s to %s", legacyDir, newDir)
		}
	}
	if err := os.MkdirAll(newDir, 0755); err != nil {
		return "", err
	}
	return newDir, nil
}

// platformBase uses a local Windows profile for the database and a separate
// cache directory, while preserving macOS's existing paths and migrations.
func platformAppDir(home, base, name, goos string, getenv func(string) string) string {
	dir := filepath.Join(platformBase(home, base, goos, getenv), name)
	if goos == "windows" && base == appCacheBase {
		dir = filepath.Join(dir, "Cache")
	}
	return dir
}

func platformBase(home, base, goos string, getenv func(string) string) string {
	if goos == "windows" {
		local := getenv("LOCALAPPDATA")
		if local == "" || !filepath.IsAbs(local) {
			local = filepath.Join(home, "AppData", "Local")
		}
		return local
	}
	if goos == "darwin" {
		return filepath.Join(home, libraryBasePath, base)
	}
	key, fallback := "XDG_DATA_HOME", filepath.Join(home, ".local", "share")
	if base == appCacheBase {
		key, fallback = "XDG_CACHE_HOME", filepath.Join(home, ".cache")
	}
	if root := getenv(key); root != "" && filepath.IsAbs(root) {
		return root
	}
	return fallback
}
