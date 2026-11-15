package library

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	userstore "myguo/internal/store"
)

func TestReconcileMustKeepPlaybackSavedDuringFetch(t *testing.T) {
	const id = "710000000000001"
	var service *Service
	service, db := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if err := service.SaveProgress(context.Background(), id, "710000000000103", "Local", "", 3, 10, 75, 100); err != nil {
				t.Error(err)
			}
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"data_list": []map[string]any{{"series_id": id, "vid_index": 1, "current_play_time": 10000, "last_watch_time": 2000, "episode_cnt": 10}}}})
		} else {
			writeJSON(w, map[string]any{"code": 0})
		}
	}))
	if err := db.SaveHistory(HistoryItem{SeriesID: id, Title: "Local", EpisodeNum: 1, PositionSec: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileSeriesProgress(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetHistoryItem(id)
	if got.EpisodeNum != 3 || got.PositionSec != 75 {
		t.Fatalf("new playback overwritten: episode=%d position=%g updatedAt=%d", got.EpisodeNum, got.PositionSec, got.UpdatedAt)
	}
}
func TestReconcileMustDiscardPreviousAccountResponse(t *testing.T) {
	const id = "710000000000001"
	var service *Service
	var db *userstore.Store
	cookie := "sessionid=A"
	service, db = newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie = "sessionid=B"
		if err := db.ClearUserData(); err != nil {
			t.Error(err)
		}
		service.InvalidateUserData()
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"data_list": []map[string]any{{"series_id": id, "vid_index": 1, "current_play_time": 10000, "last_watch_time": 2000}}}})
	}))
	service.credentials = func() (string, string) { return cookie, "" }
	service.InvalidateUserData()
	if _, err := service.ReconcileSeriesProgress(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got, ok := db.GetHistoryItem(id); ok {
		t.Fatalf("account A response inserted after account B became current: %+v", got)
	}
}
func TestReconcileMustNotReuseDifferentEpisodeDuration(t *testing.T) {
	const id = "710000000000001"
	service, db := newSyncTestService(t, seriesProgressHandler([]map[string]any{{"series_id": id, "vid_index": 9, "current_play_time": 19000, "last_watch_time": 2000, "episode_cnt": 10}}))
	if err := db.SaveHistory(HistoryItem{SeriesID: id, Title: "Local", EpisodeNum: 1, DurationSec: 20, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := service.ReconcileSeriesProgress(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.DurationSec != 0 {
		t.Fatalf("episode 10 inherited episode 1 duration: position=%g duration=%g", got.PositionSec, got.DurationSec)
	}
}
func TestBackoffMustProtectUnsyncedHistoryFromSnapshot(t *testing.T) {
	service, db := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeJSON(w, map[string]any{"code": 100103})
			return
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"data_list": []any{}, "has_more": false}})
	}))
	seedQueuedHistoryUpload(t, db)
	if _, err := service.processPendingHistoryUploads(); err == nil {
		t.Fatal("expected rejected upload")
	}
	service.historyMutationAt = time.Now().Add(-6 * time.Second)
	if err := service.syncRemoteHistory(context.Background(), service.currentHistoryGeneration()); err != nil {
		t.Fatal(err)
	}
	items, err := db.GetHistory()
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := db.PendingHistoryUploads(100)
	if len(items) != 1 {
		t.Fatalf("unsynced local history deleted during backoff: rows=%d pending=%d", len(items), len(pending))
	}
}
func TestHistoryWorkerMustDrainMoreThanOnePage(t *testing.T) {
	service, db := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{"code": 0}) }))
	for i := 0; i < 21; i++ {
		item := HistoryItem{SeriesID: fmt.Sprint(710000000000001 + int64(i)), Title: "Local", EpisodeNum: 1, UpdatedAt: 1}
		if err := db.SaveHistory(item); err != nil {
			t.Fatal(err)
		}
		if err := db.QueueHistoryUpload(item, fmt.Sprint(710000000000101+int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	processed, err := service.syncPendingHistoryMutations()
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := db.PendingHistoryUploads(100)
	if len(pending) != 0 {
		t.Fatalf("single sync left a page tail behind: processed=%d pending=%d", processed, len(pending))
	}
}
func TestLikesMustRejectMissingList(t *testing.T) {
	service, db := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{}})
	}))
	if err := db.ReplaceLikeBatch([]LikeItem{{SeriesID: "710000000000001", EpisodeID: "710000000000101", Title: "Local"}}); err != nil {
		t.Fatal(err)
	}
	err := service.syncRemoteLikes(context.Background(), service.currentLikeGeneration())
	if err == nil {
		t.Fatal("incomplete likes response was accepted")
	}
	items, _ := db.GetLikes()
	if len(items) != 1 {
		t.Fatalf("missing likes list erased existing rows: rows=%d syncErr=%v", len(items), err)
	}
}
func TestReservationsMustRejectMissingList(t *testing.T) {
	service, db := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{}})
	}))
	if err := db.ReplaceReservationBatch([]ReservationItem{{SeriesID: "710000000000001", Title: "Local"}}); err != nil {
		t.Fatal(err)
	}
	_, err := service.syncRemoteReservations(context.Background(), service.currentReservationGeneration())
	if err == nil {
		t.Fatal("incomplete reservation response was accepted")
	}
	items, _ := db.GetReservations()
	if len(items) != 1 {
		t.Fatalf("missing reservation lists erased existing rows: rows=%d syncErr=%v", len(items), err)
	}
}

func TestInitialHistoryMustSurfaceRemoteFailure(t *testing.T) {
	service, _ := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	items, err := service.GetHistory(context.Background())
	if err == nil {
		t.Fatalf("initial cloud failure reported as a successful empty library: rows=%d error=%v", len(items), err)
	}
}

func TestBackoffHistoryMustWinEvenIfRemoteTimestampIsNewer(t *testing.T) {
	service, db := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeJSON(w, map[string]any{"code": 100103})
			return
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"data_list": []map[string]any{{
			"series_id": historyRefreshTestItem().SeriesID, "vid_index": 0, "current_play_time": 1000, "last_watch_time": time.Now().Add(time.Hour).UnixMilli(),
		}}, "has_more": false}})
	}))
	seedQueuedHistoryUpload(t, db)
	if _, err := service.processPendingHistoryUploads(); err == nil {
		t.Fatal("expected upload failure")
	}
	service.historyMutationAt = time.Now().Add(-6 * time.Second)
	if err := service.syncRemoteHistory(context.Background(), service.currentHistoryGeneration()); err != nil {
		t.Fatal(err)
	}
	assertLocalHistoryPreserved(t, db)
}

func TestHistoryWorkerSkipsBackoffPageAndUploadsReadyTail(t *testing.T) {
	service, db := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{"code": 0}) }))
	for i := 0; i < 21; i++ {
		item := HistoryItem{SeriesID: fmt.Sprint(710000000000001 + int64(i)), EpisodeNum: 1, UpdatedAt: int64(i + 1)}
		if err := db.QueueHistoryUpload(item, fmt.Sprint(710000000000101+int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	firstPage, err := db.PendingHistoryUploads(20)
	if err != nil {
		t.Fatal(err)
	}
	service.markHistoryUploadFailed(firstPage, fmt.Errorf("network failure"))
	processed, err := service.syncPendingHistoryMutations()
	if err != nil {
		t.Fatal(err)
	}
	pending, err := db.PendingHistoryUploads(100)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 || len(pending) != 20 {
		t.Fatalf("ready tail starved: processed=%d pending=%d", processed, len(pending))
	}
}

func TestReconcileRejectsResponseAfterStoreAccountTransaction(t *testing.T) {
	var db *userstore.Store
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate the point after the account transaction but before credentials
		// and service invalidation have been published to the rest of the process.
		if err := db.SetAuthenticatedAccount(userstore.AccountAuth{Cookies: "sessionid=B"}, userstore.UserProfile{UserID: "B", LoggedIn: true}, true); err != nil {
			t.Error(err)
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"data_list": []map[string]any{{"series_id": "710000000000001", "vid_index": 2, "current_play_time": 20000, "last_watch_time": time.Now().UnixMilli()}}}})
	}))
	db = store
	if _, err := service.ReconcileSeriesProgress(context.Background(), "710000000000001"); err != nil {
		t.Fatal(err)
	}
	if items, err := db.GetHistory(); err != nil || len(items) != 0 {
		t.Fatalf("stale response crossed store account transaction: items=%+v err=%v", items, err)
	}
}

func TestAccountListsDistinguishInvalidResponseAndExplicitEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"missing status", `{"data":{"mixed_data_list":[],"subscribe_items":[]}}`, false},
		{"missing data", `{"code":0}`, false},
		{"null lists", `{"code":0,"data":{"mixed_data_list":null,"subscribe_items":null}}`, false},
		{"business failure", `{"code":1,"data":{"mixed_data_list":[],"subscribe_items":[]}}`, false},
		{"incomplete pagination", `{"code":0,"data":{"mixed_data_list":[],"subscribe_items":[],"has_more":true}}`, false},
		{"explicit empty", `{"code":0,"data":{"mixed_data_list":[],"subscribe_items":[]}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, db := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			if err := db.ReplaceLikeBatch([]LikeItem{{SeriesID: "710000000000001", EpisodeID: "710000000000101", Title: "Local"}}); err != nil {
				t.Fatal(err)
			}
			if err := db.ReplaceReservationBatch([]ReservationItem{{SeriesID: "710000000000001", Title: "Local"}}); err != nil {
				t.Fatal(err)
			}
			likeErr := service.syncRemoteLikes(context.Background(), service.currentLikeGeneration())
			_, reservationErr := service.syncRemoteReservations(context.Background(), service.currentReservationGeneration())
			likes, _ := db.GetLikes()
			reservations, _ := db.GetReservations()
			if tc.valid {
				if likeErr != nil || reservationErr != nil || len(likes) != 0 || len(reservations) != 0 {
					t.Fatalf("explicit empty rejected: likes=%d reservations=%d errors=%v / %v", len(likes), len(reservations), likeErr, reservationErr)
				}
			} else if likeErr == nil || reservationErr == nil || len(likes) != 1 || len(reservations) != 1 {
				t.Fatalf("invalid response changed local data: likes=%d reservations=%d errors=%v / %v", len(likes), len(reservations), likeErr, reservationErr)
			}
		})
	}
}

func TestAnonymousEmptyHistoryDoesNotRequireNetwork(t *testing.T) {
	service, _ := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("anonymous history tried to use account API") }))
	service.credentials = func() (string, string) { return "", "" }
	if items, err := service.GetHistory(context.Background()); err != nil || len(items) != 0 {
		t.Fatalf("anonymous empty history = %+v, %v", items, err)
	}
}

func TestDelayedNativeProgressCannotEnterTheNextAccount(t *testing.T) {
	service, db := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{"code": 0}) }))
	scope := service.PlaybackScope()
	service.credentials = func() (string, string) { return "sessionid=next-account", "" }
	service.InvalidateUserData()
	err := service.SaveProgressForScope(context.Background(), scope, "710000000000001", "710000000000103", "旧账号视频", "", 3, 10, 88, 100)
	if err == nil {
		t.Fatal("accepted progress queued by the previous account")
	}
	if item, ok := db.GetHistoryItem("710000000000001"); ok {
		t.Fatalf("old playback leaked to new account: %+v", item)
	}
}

func TestDelayedNativeInteractionKeepsItsOriginalIdentity(t *testing.T) {
	service, db := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("stale interaction reached remote API") }))
	ctx := service.BindPlaybackContext(context.Background(), service.PlaybackScope())
	service.credentials = func() (string, string) { return "sessionid=B", "" }
	service.InvalidateUserData()
	const id = "710000000000001"
	if _, err := service.ToggleFavorite(ctx, id, "old", "", "", 1); err == nil {
		t.Fatal("stale scene used new favorite owner")
	}
	if _, err := service.ToggleLike(ctx, id, "710000000000101", "old", "", 1); err == nil {
		t.Fatal("stale scene used new like owner")
	}
	if _, _, err := service.PlaybackCredentials(ctx); err == nil {
		t.Fatal("stale scene could obtain new credentials")
	}
	if db.IsFavorite(id) || db.IsLiked(id, "710000000000101", 1) {
		t.Fatal("stale local interaction accepted")
	}
}
