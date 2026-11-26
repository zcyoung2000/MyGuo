package ipc

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	userstore "myguo/internal/store"
)

func TestSettingsIPCReportsStorageDeviceAndPreservesSignedInAccount(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	appDir := t.TempDir()
	home := t.TempDir()
	cacheDir := t.TempDir()
	media := newTestMediaServer(t, cacheDir)
	cacheDir = media.CacheDir()
	if err := os.WriteFile(filepath.Join(cacheDir, "episode.bin"), []byte(strings.Repeat("media", 1024)), 0600); err != nil {
		t.Fatal(err)
	}
	store, storeErr := userstore.NewStore(appDir)
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := newHongguoService(nil, store, media, appDir, home)
	t.Cleanup(func() { CloseService(service) })
	service.httpClient = &http.Client{Transport: ipcHandlerTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "user/info") {
			_, _ = w.Write([]byte(`{"code":0,"data":{"user_id":"710000000000001","user_name":"Test account"}}`))
			return
		}
		http.NotFound(w, r)
	})}}

	size, err := service.GetCacheSize(context.Background())
	if err != nil || size != 5120 {
		t.Fatalf("GetCacheSize = %d, %v; want 5120", size, err)
	}
	device, err := service.GetDeviceInfo(context.Background())
	if err != nil || device["deviceId"] == "" || device["version"] != "2.0.0" || device["arch"] == "" {
		t.Fatalf("GetDeviceInfo = %+v, %v", device, err)
	}
	if _, err := service.LoginWithCookies(context.Background(), "sessionid=current", ""); err != nil {
		t.Fatalf("LoginWithCookies failed: %v", err)
	}
	profile, err := service.GetProfile(context.Background())
	if err != nil || !profile.LoggedIn || profile.Nickname != "Test account" || profile.UserID != "710000000000001" {
		t.Fatalf("profile after login = %+v, %v", profile, err)
	}
	cleared, err := service.ClearCache(context.Background())
	if err != nil || cleared != 5120 {
		t.Fatalf("ClearCache = %d, %v", cleared, err)
	}
	if remaining := media.GetCacheSize(); remaining != 0 {
		t.Fatalf("cache remaining = %d", remaining)
	}
	if _, ok := service.store.GetAccountAuth(); !ok {
		t.Fatal("ClearCache must not clear account credentials")
	}
}

func TestSettingsIPCLoginValidationKeepsUnauthenticatedProfile(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"user_id":"0","user_name":"anonymous"}}`))
	})
	store, storeErr := userstore.NewStore(t.TempDir())
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := newHongguoService(nil, store, nil, t.TempDir(), t.TempDir())
	t.Cleanup(func() { CloseService(service) })
	service.httpClient = &http.Client{Transport: ipcHandlerTransport{handler: handler}}
	if _, err := service.LoginWithCookies(context.Background(), "sessionid=test", ""); err == nil {
		t.Fatal("anonymous user info was accepted as login")
	}
	profile, err := service.GetProfile(context.Background())
	if err != nil || profile.LoggedIn || profile.UserID != "anonymous" {
		t.Fatalf("failed login changed visible profile: %+v, %v", profile, err)
	}
	if _, err := service.RefreshUserProfile(context.Background()); err == nil {
		t.Fatal("refresh succeeded without login")
	}
}

func newTestMediaServer(t *testing.T, cacheDir string) *MediaServer {
	t.Helper()
	oldHome, hadHome := os.LookupEnv("HOME")
	t.Setenv("HOME", cacheDir)
	if runtime.GOOS == "windows" {
		// Windows resolves the cache root through LOCALAPPDATA, ignoring HOME.
		// Redirect it so tests neither read nor write the real user cache.
		t.Setenv("LOCALAPPDATA", cacheDir)
	}
	ms, err := NewMediaServer(nil)
	if hadHome {
		t.Setenv("HOME", oldHome)
	} else {
		os.Unsetenv("HOME")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ms.Close)
	return ms
}
