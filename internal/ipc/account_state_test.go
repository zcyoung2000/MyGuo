package ipc

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"myguo/internal/resourcecache"
	userstore "myguo/internal/store"
)

func accountTestService(t *testing.T, appDir, home string) (*HongguoService, *Store) {
	t.Helper()
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	store, err := NewStore(appDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := newHongguoService(nil, store, nil, appDir, home)
	t.Cleanup(func() { CloseService(service) })
	service.httpClient = &http.Client{Transport: ipcHandlerTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"user_id":"710000000000099","user_name":"Test account"}}`))
	})}}
	return service, store
}

func TestLogoutRestartDoesNotImportLegacyCredentials(t *testing.T) {
	home := t.TempDir()
	legacyPath := filepath.Join(home, "Library", "Application Support", "hgplayer", "account.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0755); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"cookies":"sessionid=legacy","profile":{"userId":"710000000000088","nickname":"Legacy","loggedIn":true}}`)
	if err := os.WriteFile(legacyPath, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	appDir := t.TempDir()
	service, store := accountTestService(t, appDir, home)
	if cookies, _ := service.authCredentials(); cookies != "" {
		t.Fatal("startup imported another app's credentials")
	}
	if _, err := service.LoginWithCookies(context.Background(), "sessionid=current", "token"); err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := newHongguoService(nil, store, nil, appDir, home)
	t.Cleanup(func() { CloseService(restarted) })
	if cookies, _ := restarted.authCredentials(); cookies != "" || store.GetProfile().LoggedIn {
		t.Fatal("explicit logout did not survive restart with a legacy account present")
	}
	if data, err := os.ReadFile(legacyPath); err != nil || !bytes.Equal(data, legacy) {
		t.Fatal("logout changed the other app's account")
	}
}

func TestLoginPersistenceFailureDoesNotPublishSession(t *testing.T) {
	appDir := t.TempDir()
	service, store := accountTestService(t, appDir, t.TempDir())
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	profile, err := service.LoginWithCookies(context.Background(), "sessionid=new", "token")
	if err == nil || profile != nil {
		t.Fatalf("failed persistence reported success: profile=%v err=%v", profile, err)
	}
	if cookies, _ := service.authCredentials(); cookies != "" || store.GetProfile().LoggedIn {
		t.Fatal("failed persistence changed the active account")
	}
}

func TestLoginDatabaseFailureRestoresPreviousCredentials(t *testing.T) {
	service, store := accountTestService(t, t.TempDir(), t.TempDir())
	if err := store.SetAuthenticatedAccount(userstore.AccountAuth{
		Cookies: "sessionid=previous",
		Token:   "previous-token",
	}, UserProfile{
		UserID: "710000000000001", Nickname: "Previous", LoggedIn: true,
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := service.restoreSavedAccount(); err != nil {
		t.Fatal(err)
	}
	if cookies, _ := service.authCredentials(); cookies != "sessionid=previous" {
		t.Fatal("stored credentials were not restored")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if profile, err := service.LoginWithCookies(context.Background(), "sessionid=new", "token"); err == nil || profile != nil {
		t.Fatal("database failure reported login success")
	}
	if cookies, _ := service.authCredentials(); cookies != "sessionid=previous" {
		t.Fatal("failed login replaced the in-memory session")
	}
}

func TestSavedAccountRestoresAndUsesPrivateFilePermissions(t *testing.T) {
	appDir := t.TempDir()
	home := t.TempDir()
	service, store := accountTestService(t, appDir, home)
	if _, err := service.LoginWithCookies(context.Background(), "sessionid=current", "token"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(appDir, "account.json")); !os.IsNotExist(err) {
		t.Fatalf("account.json exists, err = %v", err)
	}
	info, err := os.Stat(filepath.Join(appDir, "userdata.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Windows FileMode does not expose Unix permission bits; private ACLs are
	// handled by the OS instead. Only the POSIX branch asserts 0600.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("database permissions = %v", info.Mode().Perm())
	}
	restarted := newHongguoService(nil, store, nil, appDir, home)
	t.Cleanup(func() { CloseService(restarted) })
	if cookies, token := restarted.authCredentials(); cookies != "sessionid=current" || token != "token" || !store.GetProfile().LoggedIn {
		t.Fatal("saved session was not restored")
	}
}

func TestMissingCredentialsClearStaleLoggedInProfile(t *testing.T) {
	service, store := accountTestService(t, t.TempDir(), t.TempDir())
	if err := store.UpdateProfile(UserProfile{UserID: "710000000000001", Nickname: "Previous", DeviceID: "device", LoggedIn: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory(HistoryItem{SeriesID: "710000000000002", EpisodeNum: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.restoreSavedAccount(); err != nil {
		t.Fatal(err)
	}
	profile := store.GetProfile()
	if profile.LoggedIn || profile.UserID != "anonymous" || profile.DeviceID != "device" {
		t.Fatalf("stale profile survived: %+v", profile)
	}
	if items, err := store.GetHistory(); err != nil || len(items) != 0 {
		t.Fatalf("stale visible account data survived: %v, %v", items, err)
	}
}

func TestLogoutClearsVisibleAccountData(t *testing.T) {
	service, store := accountTestService(t, t.TempDir(), t.TempDir())
	if _, err := service.LoginWithCookies(context.Background(), "sessionid=current", "token"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory(HistoryItem{SeriesID: "710000000000002", EpisodeNum: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if items, err := store.GetHistory(); err != nil || len(items) != 0 {
		t.Fatalf("visible account data survived logout: %v, %v", items, err)
	}
}

func TestAnonymousGuestLibraryIsPreservedWithoutCredentials(t *testing.T) {
	service, store := accountTestService(t, t.TempDir(), t.TempDir())
	if err := store.SaveHistory(HistoryItem{SeriesID: "710000000000002", EpisodeNum: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.restoreSavedAccount(); err != nil {
		t.Fatal(err)
	}
	if items, err := store.GetHistory(); err != nil || len(items) != 1 {
		t.Fatalf("guest library = %v, %v; want one local row", items, err)
	}
}

func TestSeriesPageRefreshesUserFlagsWithoutMutatingCache(t *testing.T) {
	service, store := accountTestService(t, t.TempDir(), t.TempDir())
	const id = "710000000000001"
	cached := &SeriesDetailPageDTO{SeriesDetailDTO: SeriesDetailDTO{ID: id, Title: "Catalog metadata"}}
	service.cache.Set("series_page:"+id, resourcecache.Item{Value: cached, ExpiresAt: time.Now().Add(5 * time.Minute)})
	if _, err := service.ToggleFavorite(context.Background(), id, "Catalog metadata", "", "", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ToggleReservation(ReservationItem{SeriesID: id, Title: "Catalog metadata"}); err != nil {
		t.Fatal(err)
	}
	page, err := service.GetSeriesPage(context.Background(), id)
	if err != nil || !page.IsFavorite || !page.Reserved || page.Title != cached.Title {
		t.Fatalf("stale user flags: %+v, %v", page, err)
	}
	if cached.IsFavorite || cached.Reserved || page == cached {
		t.Fatal("response mutated the shared catalog cache")
	}
	if _, err := service.ToggleFavorite(context.Background(), id, "Catalog metadata", "", "", 10); err != nil {
		t.Fatal(err)
	}
	page, err = service.GetSeriesPage(context.Background(), id)
	if err != nil || page.IsFavorite {
		t.Fatal("unfavorite did not refresh the cached detail response")
	}
}

func TestProfileRefreshCanonicalizesLegacyIdentityWithoutClearingHistory(t *testing.T) {
	for _, alreadyCanonical := range []bool{false, true} {
		t.Run(fmt.Sprint(alreadyCanonical), func(t *testing.T) {
			s, store := accountTestService(t, t.TempDir(), t.TempDir())
			oldID := "710000000000001"
			canonical := "710000000000099"
			profileID := oldID
			if alreadyCanonical {
				profileID = canonical
			}
			cookie := "sessionid=same"
			if err := store.SetAuthenticatedAccount(userstore.AccountAuth{Cookies: cookie}, UserProfile{UserID: profileID, LoggedIn: true}, false); err != nil {
				t.Fatal(err)
			}
			if err := s.restoreSavedAccount(); err != nil {
				t.Fatal(err)
			}
			item := HistoryItem{SeriesID: "710000000000002", Title: "已有观看记录", UpdatedAt: 100, EpisodeNum: 3, PositionSec: 45}
			if err := store.QueueHistoryUpload(item, "710000000000102", "user:"+profileID); err != nil {
				t.Fatal(err)
			}
			if !alreadyCanonical {
				if err := store.SaveHistory(item, "user:"+oldID); err != nil {
					t.Fatal(err)
				}
			}
			s.httpClient = &http.Client{Transport: ipcHandlerTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"code":0,"data":{"user_id":"0","biz_user_id":"710000000000099","req_id":710000000000001,"user_name":"Same account"}}`))
			})}}
			if _, err := s.RefreshUserProfile(context.Background()); err != nil {
				t.Fatal(err)
			}
			rows, _ := store.GetHistory()
			if len(rows) != 1 || rows[0].PositionSec != 45 {
				t.Fatal("same-credential refresh lost history")
			}
			pending, _ := store.PendingHistoryUploads(10, "user:"+canonical)
			if len(pending) != 1 {
				t.Fatal("legacy queue remained stranded")
			}
		})
	}
}

func TestCanonicalAccountDoesNotAdoptUnprovenLegacyQueue(t *testing.T) {
	s, db := accountTestService(t, t.TempDir(), t.TempDir())
	if err := db.SetAuthenticatedAccount(userstore.AccountAuth{Cookies: "sessionid=current"}, UserProfile{UserID: "710000000000099", LoggedIn: true}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreSavedAccount(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueueHistoryUpload(HistoryItem{SeriesID: "710000000000002", UpdatedAt: 100}, "710000000000102", "user:710000000000001"); err != nil {
		t.Fatal(err)
	}
	s.httpClient = &http.Client{Transport: ipcHandlerTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"biz_user_id":"710000000000099","req_id":710000000000001}}`))
	})}}
	if _, err := s.RefreshUserProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := db.GetHistory()
	if err != nil || len(rows) != 0 {
		t.Fatalf("unproven legacy history was adopted: %v %v", rows, err)
	}
	old, err := db.PendingHistoryUploads(10, "user:710000000000001")
	if err != nil || len(old) != 1 {
		t.Fatal("unproven queue was removed")
	}
}

func TestChangedCredentialCannotClaimLegacyRequestID(t *testing.T) {
	s, store := accountTestService(t, t.TempDir(), t.TempDir())
	oldID := "710000000000001"
	store.SetAuthenticatedAccount(userstore.AccountAuth{Cookies: "sessionid=old"}, UserProfile{UserID: oldID, LoggedIn: true}, false)
	s.restoreSavedAccount()
	item := HistoryItem{SeriesID: "710000000000002", UpdatedAt: 100}
	store.SaveHistory(item)
	store.QueueHistoryUpload(item, "710000000000102", "user:"+oldID)
	s.httpClient = &http.Client{Transport: ipcHandlerTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"biz_user_id":"710000000000099","req_id":710000000000001}}`))
	})}}
	if _, err := s.LoginWithCookies(context.Background(), "sessionid=different", ""); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.GetHistory()
	old, _ := store.PendingHistoryUploads(10, "user:"+oldID)
	if len(rows) != 0 || len(old) != 1 {
		t.Fatal("new credential adopted an unverified legacy account")
	}
}
