package library

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	userstore "myguo/internal/store"
)

func TestInvalidPendingEpisodeDoesNotBlockValidHistoryUpload(t *testing.T) {
	var requests atomic.Int32
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeJSON(w, map[string]any{"code": 0})
	}))
	for i, row := range []struct{ seriesID, episodeID string }{
		{"710000000000001", ""},
		{"710000000000002", "710000000000102"},
	} {
		if err := store.QueueHistoryUpload(HistoryItem{SeriesID: row.seriesID, EpisodeNum: 1, UpdatedAt: int64(i + 1)}, row.episodeID); err != nil {
			t.Fatal(err)
		}
	}
	processed, err := service.syncPendingHistoryMutations()
	if err == nil {
		t.Fatal("unresolvable history intent did not report its retained error")
	}
	pending, queueErr := store.PendingHistoryUploads(10)
	if queueErr != nil || processed != 1 || requests.Load() != 1 || len(pending) != 1 || pending[0].SeriesID != "710000000000001" {
		t.Fatalf("sync processed=%d requests=%d pending=%+v queueErr=%v syncErr=%v", processed, requests.Load(), pending, queueErr, err)
	}
}

func TestRejectedHistoryItemDoesNotReplayAcceptedSubBatches(t *testing.T) {
	var accepted atomic.Int32
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		books := decodeGZIP(t, raw)["books"].([]any)
		for _, value := range books {
			book := value.(map[string]any)
			if book["book_id"] == "710000000000004" {
				writeJSON(w, map[string]any{"code": 100103})
				return
			}
		}
		accepted.Add(int32(len(books)))
		writeJSON(w, map[string]any{"code": 0})
	}))
	for i := 1; i <= 4; i++ {
		if err := store.QueueHistoryUpload(HistoryItem{SeriesID: fmt.Sprintf("71000000000000%d", i), EpisodeNum: 1, UpdatedAt: int64(i)}, "710000000000101"); err != nil {
			t.Fatal(err)
		}
	}
	processed, err := service.syncPendingHistoryMutations()
	if err == nil {
		t.Fatal("rejected record did not remain visible as a sync error")
	}
	pending, queueErr := store.PendingHistoryUploads(10)
	if queueErr != nil || accepted.Load() != 3 || processed != 3 || len(pending) != 1 || pending[0].SeriesID != "710000000000004" {
		t.Fatalf("accepted=%d processed=%d pending=%+v queueErr=%v syncErr=%v", accepted.Load(), processed, pending, queueErr, err)
	}
}

func TestRejectedHistoryDeleteDoesNotReplayAcceptedSubBatches(t *testing.T) {
	var accepted atomic.Int32
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		updates := decodeGZIP(t, raw)["update_datas"].([]any)
		for _, value := range updates {
			if value.(map[string]any)["book_id"] == float64(710000000000003) {
				writeJSON(w, map[string]any{"code": 100103})
				return
			}
		}
		accepted.Add(int32(len(updates)))
		writeJSON(w, map[string]any{"code": 0})
	}))
	for i := 1; i <= 3; i++ {
		seriesID := fmt.Sprintf("71000000000000%d", i)
		if err := store.SaveHistory(HistoryItem{SeriesID: seriesID, EpisodeNum: 1}); err != nil {
			t.Fatal(err)
		}
		if err := store.RememberEpisode(seriesID, "710000000000101", 1, "720p"); err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteHistory(seriesID); err != nil {
			t.Fatal(err)
		}
	}
	processed, err := service.syncPendingHistoryMutations()
	if err == nil {
		t.Fatal("rejected delete did not remain visible as a sync error")
	}
	pending, queueErr := store.PendingHistoryDeletes(10)
	if queueErr != nil || accepted.Load() != 2 || processed != 2 || len(pending) != 1 || pending[0].SeriesID != "710000000000003" {
		t.Fatalf("accepted=%d processed=%d pending=%+v queueErr=%v syncErr=%v", accepted.Load(), processed, pending, queueErr, err)
	}
}

func TestRateLimitCooldownAppliesAcrossVideosInAccount(t *testing.T) {
	var requests atomic.Int32
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeJSON(w, map[string]any{"code": 100, "message": "FAST_REJECT"})
	}))
	for i := 1; i <= 2; i++ {
		if err := store.QueueHistoryUpload(HistoryItem{SeriesID: fmt.Sprintf("71000000000000%d", i), EpisodeNum: 1, UpdatedAt: int64(i)}, fmt.Sprintf("71000000000010%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.syncPendingHistoryMutations(); err == nil {
		t.Fatal("first server rate limit was not surfaced")
	}
	if _, err := service.syncPendingHistoryMutations(); err != nil {
		t.Fatalf("account cooldown should defer without issuing another request: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests during account cooldown = %d, want one", requests.Load())
	}
}

func TestCloudRefreshMergesUnrelatedPlaybackSavedDuringFetch(t *testing.T) {
	const localID, remoteID = "710000000000001", "710000000000002"
	var service *Service
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "read_progress/upload") {
			writeJSON(w, map[string]any{"code": 0})
			return
		}
		if err := service.SaveProgress(context.Background(), localID, "710000000000101", "Playing locally", "", 1, 10, 12, 100); err != nil {
			t.Error(err)
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"data_list": []map[string]any{{
			"series_id": remoteID, "vid_index": 2, "current_play_time": 22_000, "last_watch_time": time.Now().UnixMilli(),
		}}, "has_more": false}})
	}))
	if err := store.SaveHistory(HistoryItem{SeriesID: localID, Title: "Before playback", EpisodeNum: 1, PositionSec: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.syncRemoteHistory(context.Background(), service.currentHistoryGeneration()); err != nil {
		t.Fatal(err)
	}
	service.WaitForBackgroundSync()
	local, localOK := store.GetHistoryItem(localID)
	remote, remoteOK := store.GetHistoryItem(remoteID)
	pending, err := store.PendingHistoryUploads(10)
	if !localOK || local.PositionSec != 12 || !remoteOK || remote.PositionSec != 22 || err != nil || len(pending) != 0 {
		t.Fatalf("local=%+v/%t remote=%+v/%t pending=%+v err=%v", local, localOK, remote, remoteOK, pending, err)
	}
}

func TestPendingHistoryUploadRetriesWhenBackoffExpires(t *testing.T) {
	var requests atomic.Int32
	retried := make(chan struct{}, 1)
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, map[string]any{"code": 0})
		retried <- struct{}{}
	}))
	item := HistoryItem{SeriesID: "710000000000001", EpisodeNum: 1, UpdatedAt: 1}
	if err := store.QueueHistoryUpload(item, "710000000000101"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.syncPendingHistoryMutations(); err == nil {
		t.Fatal("first upload unexpectedly succeeded")
	}
	pending, err := store.PendingHistoryUploads(1)
	if err != nil || len(pending) != 1 {
		t.Fatalf("failed upload queue = %+v, %v", pending, err)
	}
	service.historyRetryMu.Lock()
	service.historyUploadRetries[historyUploadKey(pending[0])] = historyUploadRetry{attempt: 1, nextAt: time.Now().Add(50 * time.Millisecond)}
	service.historyRetryMu.Unlock()
	service.schedulePendingHistorySync()
	select {
	case <-retried:
	case <-time.After(3 * time.Second):
		t.Fatal("pending upload was not retried after its cooldown")
	}
	service.WaitForBackgroundSync()
	pending, err = store.PendingHistoryUploads(10)
	if err != nil || len(pending) != 0 || requests.Load() != 2 {
		t.Fatalf("retry requests=%d pending=%+v err=%v", requests.Load(), pending, err)
	}
}

func TestPendingHistoryRetriesResumeOnServiceRestart(t *testing.T) {
	store, err := userstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.SetSyncScope("user:restart")
	if err := store.QueueHistoryUpload(HistoryItem{SeriesID: "710000000000001", EpisodeNum: 1}, "710000000000101", "user:restart"); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	service := NewService(Dependencies{
		Store:       store,
		AuthSession: func() (string, string, string) { return "restart", "sessionid=restart", "token" },
		HTTPClient: &http.Client{Transport: handlerTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			writeJSON(w, map[string]any{"code": 0})
		})}},
	})
	defer service.Close()
	if pending, err := store.PendingHistoryUploads(10); err != nil || len(pending) != 1 {
		t.Fatalf("startup queue = %+v, err=%v", pending, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pending, queryErr := store.PendingHistoryUploads(10)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if len(pending) == 0 {
			if requests.Load() != 1 {
				t.Fatalf("startup retries made %d requests, want one", requests.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("service startup did not resume a durable history upload")
}
