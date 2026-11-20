package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/model"
	"myguo/internal/hgapi/protocol"
	userstore "myguo/internal/store"
)

type failingTransport struct {
	mu          sync.Mutex
	failedPosts int
	fallback    http.RoundTripper
}

func (transport *failingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPost {
		transport.mu.Lock()
		transport.failedPosts++
		transport.mu.Unlock()
		return nil, fmt.Errorf(`Post "%s": net/http: TLS handshake timeout`, request.URL)
	}
	if transport.fallback == nil {
		return nil, fmt.Errorf("missing fallback transport for %s", request.URL.Path)
	}
	return transport.fallback.RoundTrip(request)
}

func historyRefreshTestItem() HistoryItem {
	return HistoryItem{
		SeriesID:      "710000000000001",
		Title:         "本地真实历史",
		CoverURL:      "https://media.invalid/cover.jpg",
		EpisodeNum:    3,
		EpisodeTitle:  "第3集",
		PositionSec:   45,
		DurationSec:   120,
		TotalEpisodes: 20,
		UpdatedAt:     time.Now().Unix() - 60,
	}
}

func seedQueuedHistoryUpload(t *testing.T, store *userstore.Store) {
	t.Helper()
	item := historyRefreshTestItem()
	if err := store.SaveHistory(item); err != nil {
		t.Fatalf("SaveHistory() error = %v", err)
	}
	if err := store.QueueHistoryUpload(item, "710000000000103"); err != nil {
		t.Fatalf("QueueHistoryUpload() error = %v", err)
	}
}

func assertLocalHistoryPreserved(t *testing.T, store *userstore.Store) {
	t.Helper()
	items, err := store.GetHistory()
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	want := historyRefreshTestItem()
	if len(items) != 1 || items[0].Title != want.Title || items[0].EpisodeNum != 3 ||
		items[0].PositionSec != 45 || items[0].DurationSec != 120 || items[0].TotalEpisodes != 20 {
		t.Fatalf("local history = %+v, want %+v", items, want)
	}
	pending, err := store.PendingHistoryUploads(10)
	if err != nil {
		t.Fatalf("PendingHistoryUploads() error = %v", err)
	}
	if len(pending) != 1 || pending[0].EpisodeID != "710000000000103" {
		t.Fatalf("failed intent = %+v, want retained episode intent", pending)
	}
}

func TestRefreshHistoryKeepsLocalHistoryWhenProgressUploadRejected(t *testing.T) {
	log := &requestLog{}
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			log.add(capturedRequest{path: r.URL.Path, body: body})
			writeJSON(w, map[string]any{"code": 100103, "message": "PARAM_INVALID"})
			return
		}
		log.add(capturedRequest{path: r.URL.Path})
		writeJSON(w, map[string]any{"data": map[string]any{"data_list": []any{}, "has_more": false}})
	}))
	seedQueuedHistoryUpload(t, store)

	items, err := service.RefreshHistory(context.Background())
	if err == nil || !strings.Contains(err.Error(), "待上传") {
		t.Fatalf("RefreshHistory() error = %v, want a visible queued-upload status", err)
	}
	if len(items) != 1 || items[0].Title != "本地真实历史" || items[0].EpisodeNum != 3 {
		t.Fatalf("RefreshHistory() = %+v, want local row", items)
	}
	assertLocalHistoryPreserved(t, store)
	calls := log.matching("read_progress/upload")
	if len(calls) != 1 {
		t.Fatalf("progress upload calls = %d, want one", len(calls))
	}
	payload := decodeGZIP(t, calls[0].body)
	books := payload["books"].([]any)
	book := books[0].(map[string]any)
	for key, want := range map[string]any{
		"book_id":                          "710000000000001",
		"item_id":                          "710000000000103",
		"vid_index":                        float64(2),
		"current_play_time":                float64(45000),
		"progress_type":                    float64(0),
		"check_timestamp":                  false,
		"player_cumulative_total_duration": float64(45000),
	} {
		if book[key] != want {
			t.Fatalf("payload %s = %#v, want %#v", key, book[key], want)
		}
	}
}

func TestRefreshHistoryKeepsLocalHistoryWhenUploadTimesOut(t *testing.T) {
	remote := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"data": map[string]any{"data_list": []any{}, "has_more": false}})
	})
	transport := &failingTransport{fallback: handlerTransport{handler: remote}}
	service, store := newSyncTestService(t, remote)
	service.httpClient = &http.Client{Transport: transport}
	seedQueuedHistoryUpload(t, store)

	items, err := service.RefreshHistory(context.Background())
	if err == nil || !strings.Contains(err.Error(), "待上传") {
		t.Fatalf("RefreshHistory() error = %v, want a visible queued-upload status", err)
	}
	if len(items) != 1 || items[0].Title != "本地真实历史" || items[0].EpisodeNum != 3 {
		t.Fatalf("RefreshHistory() = %+v, want local row", items)
	}
	assertLocalHistoryPreserved(t, store)
	if transport.failedPosts != 1 {
		t.Fatalf("failed upload count = %d, want one", transport.failedPosts)
	}
}

func TestRefreshHistoryReadsCloudAfterPendingUploadIsRejected(t *testing.T) {
	const remoteID = "710000000000002"
	log := &requestLog{}
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			log.add(capturedRequest{path: r.URL.Path, body: body})
			writeJSON(w, map[string]any{"code": 100103, "message": "PARAM_INVALID"})
			return
		}
		log.add(capturedRequest{path: r.URL.Path})
		writeJSON(w, map[string]any{"data": map[string]any{"data_list": []map[string]any{{
			"series_id": remoteID, "vid_index": 2, "episode_cnt": 12,
			"current_play_time": 33000, "last_watch_time": time.Now().UnixMilli(),
		}}, "has_more": false}})
	}))
	seedQueuedHistoryUpload(t, store)
	if err := store.SaveHistory(HistoryItem{
		SeriesID: remoteID, Title: "另一设备播放的短剧", CoverURL: "https://media.invalid/remote.jpg",
		EpisodeNum: 1, TotalEpisodes: 12, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("SaveHistory(remote) error = %v", err)
	}

	items, err := service.RefreshHistory(context.Background())
	if err == nil || !strings.Contains(err.Error(), "待上传") {
		t.Fatalf("RefreshHistory() error = %v, want a visible queued-upload status", err)
	}
	if len(items) != 2 {
		t.Fatalf("RefreshHistory() returned %d rows, want local pending and refreshed remote rows: %+v", len(items), items)
	}
	var remoteFound, pendingFound bool
	for _, item := range items {
		switch item.SeriesID {
		case remoteID:
			remoteFound = item.EpisodeNum == 3 && item.PositionSec == 33 && item.Title == "另一设备播放的短剧"
		case historyRefreshTestItem().SeriesID:
			pendingFound = item.EpisodeNum == 3 && item.PositionSec == 45
		}
	}
	if !remoteFound || !pendingFound {
		t.Fatalf("refreshed history lost cloud progress or pending local progress: %+v", items)
	}
	if len(log.matching("read_progress/list")) != 1 {
		t.Fatalf("cloud history reads = %d, want one after upload rejection", len(log.matching("read_progress/list")))
	}
	pending, err := store.PendingHistoryUploads(10)
	if err != nil {
		t.Fatalf("PendingHistoryUploads() error = %v", err)
	}
	if len(pending) != 1 || pending[0].SeriesID != historyRefreshTestItem().SeriesID || pending[0].PositionSec != 45 {
		t.Fatalf("rejected upload intent = %+v, want the local record retained", pending)
	}
}

func TestRefreshHistoryContinuesPaginationBeyondTenPages(t *testing.T) {
	const pages, rowsPerPage = 12, 50
	baseTimestamp := time.Now().UnixMilli() - 1000
	var mu sync.Mutex
	pageNumber := 0
	var store *userstore.Store
	var service *Service
	service, store = newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, map[string]any{"code": 0})
			return
		}
		mu.Lock()
		page := pageNumber
		pageNumber++
		mu.Unlock()
		rows := make([]map[string]any, 0, rowsPerPage)
		for index := range rowsPerPage {
			rowNumber := page*rowsPerPage + index
			seriesID := fmt.Sprintf("%016d", int64(7100000000000000+rowNumber))
			rows = append(rows, map[string]any{
				"series_id": seriesID, "vid_index": 1, "episode_count": 20,
				"current_play_time": 5000, "last_watch_time": baseTimestamp - int64(rowNumber)*1000,
			})
			if err := store.SaveHistory(HistoryItem{
				SeriesID: seriesID, Title: "Known series", CoverURL: "https://media.invalid/cover.jpg",
				EpisodeNum: 1, TotalEpisodes: 20, UpdatedAt: 1,
			}); err != nil {
				t.Errorf("seed history %s: %v", seriesID, err)
			}
		}
		// This fixture models the legacy row-offset envelope; the native
		// timestamp-cursor protocol has separate coverage in internal/hgapi.
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"data_list": rows, "has_more": page+1 < pages, "next_offset": int64(page+1) * rowsPerPage,
		}})
	}))

	items, err := service.RefreshHistory(context.Background())
	if err != nil {
		t.Fatalf("RefreshHistory() error = %v", err)
	}
	if len(items) != pages*rowsPerPage {
		t.Fatalf("history rows = %d, want %d", len(items), pages*rowsPerPage)
	}
	mu.Lock()
	gotPages := pageNumber
	mu.Unlock()
	if gotPages != pages {
		t.Fatalf("history pages = %d, want %d", gotPages, pages)
	}
}

func TestFailedLaterHistoryPagePersistsProgressWithoutDeletingLocalRows(t *testing.T) {
	const remoteID, localID = "710000000000001", "710000000000002"
	calls := 0
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, map[string]any{"code": 0})
			return
		}
		calls++
		if calls == 1 {
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
				"data_list": []map[string]any{{
					"series_id": remoteID, "vid_index": 2, "episode_count": 10,
					"current_play_time": 12000, "last_watch_time": time.Now().UnixMilli(),
				}}, "has_more": true, "next_offset": 100,
			}})
			return
		}
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}))
	if err := store.SaveHistory(HistoryItem{SeriesID: localID, Title: "本机已有", CoverURL: "https://media.invalid/local.jpg", EpisodeNum: 1, TotalEpisodes: 8, UpdatedAt: 100}); err != nil {
		t.Fatal(err)
	}

	items, err := service.RefreshHistory(context.Background())
	if err == nil {
		t.Fatal("later-page failure was reported as a complete sync")
	}
	got := make(map[string]HistoryItem, len(items))
	for _, item := range items {
		got[item.SeriesID] = item
	}
	if len(got) != 2 {
		t.Fatalf("partial history IDs=%v; want both fetched row and local row", mapsKeys(got))
	}
	if got[remoteID].EpisodeNum != 3 || got[remoteID].PositionSec != 12 {
		t.Fatalf("fetched progress = episode %d at %g; want episode 3 at 12", got[remoteID].EpisodeNum, got[remoteID].PositionSec)
	}
	if got[localID].Title != "本机已有" {
		t.Fatalf("local row = %+v; want it retained", got[localID])
	}
}

func TestRefreshHistoryKeepsMetadataRetriesInBackgroundWithoutWarning(t *testing.T) {
	const id = "710000000000004"
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, map[string]any{"code": 0})
			return
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"data_list": []map[string]any{{
				"series_id": id, "vid_index": 1, "episode_count": 12,
				"current_play_time": 9000, "last_watch_time": time.Now().UnixMilli(),
			}}, "has_more": false,
		}})
	}))
	service.seriesBatch = func(context.Context, []string) ([]model.SeriesDetail, error) {
		return nil, errors.New("batch detail unavailable")
	}
	service.series = func(context.Context, string) (*model.Series, error) {
		return nil, errors.New("single detail unavailable")
	}
	t.Cleanup(service.Close)

	items, err := service.RefreshHistory(context.Background())
	if err != nil {
		t.Fatalf("metadata maintenance became a user-facing error: %v", err)
	}
	if len(items) != 1 || items[0].SeriesID != id || items[0].EpisodeNum != 2 || items[0].PositionSec != 9 {
		t.Fatalf("fetched history = %+v, want the progress retained", items)
	}
	service.WaitForBackgroundSync()
	pending, err := store.PendingHistoryMetadata(10, service.currentSyncSession().scope, time.Now())
	if err != nil || len(pending) != 0 {
		t.Fatalf("due metadata queue = %+v, %v; expected retained job in backoff", pending, err)
	}
	count, err := store.PendingHistoryMetadataCount(service.currentSyncSession().scope)
	if err != nil || count != 1 {
		t.Fatalf("metadata retry queue count=%d err=%v", count, err)
	}
}

func TestUnavailableSeriesLeavesRetryQueueWithoutUserFacingError(t *testing.T) {
	const id = "710000000000005"
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, map[string]any{"code": 0})
			return
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"data_list": []map[string]any{{
				"series_id": id, "vid_index": 0, "episode_count": 8,
				"current_play_time": 4000, "last_watch_time": time.Now().UnixMilli(),
			}}, "has_more": false,
		}})
	}))
	service.seriesBatch = func(context.Context, []string) ([]model.SeriesDetail, error) {
		return nil, errors.New("batch lookup fell back")
	}
	service.series = func(context.Context, string) (*model.Series, error) {
		return nil, &hgapi.CodeError{Code: 101001, Kind: protocol.KindContentGone, Message: "该剧集已下架"}
	}
	t.Cleanup(service.Close)

	_, err := service.RefreshHistory(context.Background())
	if err != nil {
		t.Fatalf("RefreshHistory() error = %v", err)
	}
	service.WaitForBackgroundSync()
	count, err := store.PendingHistoryMetadataCount(service.currentSyncSession().scope)
	if err != nil || count != 0 {
		t.Fatalf("permanent unavailable record remains in retry queue: count=%d err=%v", count, err)
	}
	count, err = store.UnavailableHistoryMetadataCount(service.currentSyncSession().scope)
	if err != nil || count != 1 {
		t.Fatalf("unavailable status count=%d err=%v", count, err)
	}
	if err := service.historySyncStatusError(); err != nil {
		t.Fatalf("unavailable metadata became a user-facing sync error: %v", err)
	}
	if _, err := service.GetHistory(context.Background()); err != nil {
		t.Fatalf("cached history still reports an unavailable-content warning: %v", err)
	}
	rows, err := store.GetHistory()
	if err != nil || len(rows) != 1 || rows[0].EpisodeNum != 1 || rows[0].PositionSec != 4 {
		t.Fatalf("progress was not retained: rows=%+v err=%v", rows, err)
	}
}

func TestLegacyHistoryTasksAreCleanedWithoutWarningOrRemoteWrites(t *testing.T) {
	var writes atomic.Int32
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writes.Add(1)
			writeJSON(w, map[string]any{"code": 0})
			return
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"data_list": []any{}, "has_more": false}})
	}))
	local := historyRefreshTestItem()
	if err := store.SaveHistory(local); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueHistoryUpload(local, "710000000000103", ""); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		items, err := service.RefreshHistory(context.Background())
		if err != nil || len(items) != 1 || items[0].PositionSec != local.PositionSec {
			t.Fatalf("cleanup lost progress or produced warning: %+v, %v", items, err)
		}
	}
	service.WaitForBackgroundSync()
	if writes.Load() != 0 {
		t.Fatalf("unowned task was sent under the active account: %d writes", writes.Load())
	}
	if count, err := store.PendingUnownedHistoryMutationCount(); err != nil || count != 0 {
		t.Fatalf("legacy queue was not cleaned: count=%d err=%v", count, err)
	}
}

func mapsKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestFailedHistoryUploadUsesBackoffAndRetainsIntent(t *testing.T) {
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"code": 100103, "message": "PARAM_INVALID"})
	}))
	seedQueuedHistoryUpload(t, store)

	if _, err := service.processPendingHistoryUploads(); err == nil {
		t.Fatal("invalid upload accepted")
	}
	pending, err := store.PendingHistoryUploads(10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, %v; want one retained intent", pending, err)
	}
	if _, err := service.processPendingHistoryUploads(); err != nil {
		t.Fatalf("backoff caused an upstream request: %v", err)
	}
	key := historyUploadKey(pending[0])
	retry := service.historyUploadRetries[key]
	if retry.attempt != 1 {
		t.Fatalf("retry attempt = %d, want one", retry.attempt)
	}
	service.historyUploadRetries[key] = historyUploadRetry{attempt: retry.attempt, nextAt: time.Now().Add(-time.Second)}
	if _, err := service.processPendingHistoryUploads(); err == nil {
		t.Fatal("retry after backoff accepted invalid upload")
	}
	if service.historyUploadRetries[key].attempt != 2 {
		t.Fatal("failed retry was not marked")
	}
	assertLocalHistoryPreserved(t, store)
}

func TestNewPlaybackClockDoesNotBypassFailedUploadCooldown(t *testing.T) {
	var requests atomic.Int32
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeJSON(w, map[string]any{"code": 100103, "message": "PARAM_INVALID"})
	}))
	seedQueuedHistoryUpload(t, store)
	if _, err := service.processPendingHistoryUploads(); err == nil {
		t.Fatal("fixture upload must fail")
	}
	for n := 0; n < 12; n++ {
		item := historyRefreshTestItem()
		item.PositionSec += float64(n + 1)
		if _, err := store.SavePlaybackProgress(item, "710000000000103", service.PlaybackScope()); err != nil {
			t.Fatal(err)
		}
		if _, err := service.processPendingHistoryUploads(); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := store.PendingHistoryUploads(10)
	if requests.Load() != 1 || err != nil || len(pending) != 1 || pending[0].PositionSec != historyRefreshTestItem().PositionSec+12 {
		t.Fatalf("new clocks bypassed cooldown or lost latest intent: requests=%d pending=%+v err=%v", requests.Load(), pending, err)
	}
}

func TestHistoryInvalidUploadRetainsEveryIntent(t *testing.T) {
	log := &requestLog{}
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		payload := decodeGZIP(t, raw)
		updates, ok := payload["update_datas"].([]any)
		if !ok {
			updates = payload["books"].([]any)
		}
		log.add(capturedRequest{path: r.URL.Path, body: []byte(fmt.Sprint(len(updates)))})
		writeJSON(w, map[string]any{"code": 100103, "message": "PARAM_INVALID"})
	}))
	for i := 1; i <= 2; i++ {
		item := HistoryItem{
			SeriesID:      fmt.Sprintf("71000000000000%d", i),
			EpisodeNum:    i,
			PositionSec:   float64(i * 10),
			DurationSec:   100,
			TotalEpisodes: 10,
			UpdatedAt:     time.Now().Unix(),
		}
		if err := store.SaveHistory(item); err != nil {
			t.Fatalf("SaveHistory() error = %v", err)
		}
		if err := store.QueueHistoryUpload(item, fmt.Sprintf("7100000000001%02d", i)); err != nil {
			t.Fatalf("QueueHistoryUpload() error = %v", err)
		}
	}

	if _, err := service.processPendingHistoryUploads(); err == nil {
		t.Fatal("invalid upload accepted")
	}
	if pending, err := store.PendingHistoryUploads(10); err != nil || len(pending) != 2 {
		t.Fatalf("pending = %+v, %v; want both failed intents", pending, err)
	}
}
