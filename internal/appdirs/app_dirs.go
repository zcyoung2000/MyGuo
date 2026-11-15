package appdirs

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
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
		return filepath.Join(home, libraryBasePath, appCacheBase, AppName)
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
	newDir := filepath.Join(home, libraryBasePath, base, AppName)
	legacyDir := filepath.Join(home, libraryBasePath, base, legacyAppName)

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
