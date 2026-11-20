package library

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters/memstore"
	"myguo/internal/hgapi/model"
)

type historyMetadataFallbackTransport struct{}

func (historyMetadataFallbackTransport) Do(_ context.Context, request *hgapi.Request) (*hgapi.Response, error) {
	switch {
	case strings.Contains(request.URL, "/novel/player/multi_video_detail/player/v1"):
		return &hgapi.Response{StatusCode: http.StatusOK, Body: []byte(`{"code":100,"message":"FAST_REJECT"}`)}, nil
	case strings.Contains(request.URL, "/novel/player/video_detail/v1/"):
		return &hgapi.Response{StatusCode: http.StatusOK, Body: []byte(`{"code":0,"data":{"video_data":{"series_id":"710000000000003","series_title":"远端短剧","series_cover":"https://image.invalid/cover.jpg","episode_cnt":8,"video_list":[]}}}`)}, nil
	default:
		return &hgapi.Response{StatusCode: http.StatusNotFound}, nil
	}
}

func TestRemoteFinalEpisodeUsesSDKDurationAndKeepsShortSeries(t *testing.T) {
	const id = "710000000000001"
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation: %s", r.URL.Path)
		}
		writeJSON(w, map[string]any{"data": map[string]any{"data_list": []map[string]any{{"series_id": id, "vid_index": 3, "episode_cnt": 4, "current_play_time": 96000, "last_watch_time": time.Now().UnixMilli()}}}})
	}))
	service.episodes = func(context.Context, string) (*hgapi.Page[model.Episode], error) {
		return &hgapi.Page[model.Episode]{Items: []model.Episode{{Number: 4, Duration: 100 * time.Second}}}, nil
	}
	if err := store.SaveHistory(HistoryItem{SeriesID: id, Title: "Short series", CoverURL: "https://example.invalid/cover", ContentType: 1, EpisodeNum: 1, TotalEpisodes: 1, DurationSec: 50, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.syncRemoteHistory(context.Background(), service.currentHistoryGeneration()); err != nil {
		t.Fatal(err)
	}
	service.WaitForBackgroundSync()
	rows, err := store.GetHistory()
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	row := rows[0]
	if row.ContentType != 1 || row.EpisodeNum != 4 || row.TotalEpisodes != 4 || row.DurationSec != 100 || row.PositionSec != 96 {
		t.Fatalf("incorrect final episode: %+v", row)
	}
	pending, err := store.PendingHistoryDeletes(10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("short series queued for deletion: %+v %v", pending, err)
	}
}

func TestRemoteHistoryErrorCannotEraseLocalHistory(t *testing.T) {
	for _, response := range []map[string]any{{"code": 403, "data": map[string]any{}}, {"code": 0}, {"code": 0, "data": map[string]any{}}} {
		service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, response) }))
		if err := store.SaveHistory(HistoryItem{SeriesID: "710000000000001", Title: "Local", UpdatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		if err := service.syncRemoteHistory(context.Background(), service.currentHistoryGeneration()); err == nil {
			t.Fatal("invalid response accepted")
		}
		rows, err := store.GetHistory()
		if err != nil || len(rows) != 1 {
			t.Fatalf("lost local history: %+v %v", rows, err)
		}
	}
}

func TestRemoteHistoryRemainsVisibleWhenMetadataIsUnavailable(t *testing.T) {
	const id = "710000000000003"
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"data": map[string]any{"data_list": []map[string]any{{
			"series_id": id, "vid_index": 4, "episode_cnt": 8,
			"current_play_time": 27000, "last_watch_time": time.Now().UnixMilli(),
		}}, "has_more": false}})
	}))

	if err := service.syncRemoteHistory(context.Background(), service.currentHistoryGeneration()); err != nil {
		t.Fatalf("syncRemoteHistory() error = %v", err)
	}
	service.WaitForBackgroundSync()
	items, err := store.GetHistory()
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if len(items) != 1 || items[0].SeriesID != id || items[0].EpisodeNum != 5 ||
		items[0].PositionSec != 27 || items[0].TotalEpisodes != 8 {
		t.Fatalf("remote history = %+v, want the record retained without metadata", items)
	}
}

func TestRemoteHistoryUsesSingleSeriesDetailWhenBatchLookupFails(t *testing.T) {
	const id = "710000000000003"
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"data": map[string]any{"data_list": []map[string]any{{
			"series_id": id, "vid_index": 4, "episode_cnt": 8,
			"current_play_time": 27000, "last_watch_time": time.Now().UnixMilli(),
		}}, "has_more": false}})
	}))
	profiles := memstore.New()
	if err := profiles.Save(context.Background(), &hgapi.DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002"}); err != nil {
		t.Fatal(err)
	}
	client, err := hgapi.NewClient(hgapi.Options{
		Transport: historyMetadataFallbackTransport{}, ProfileStore: profiles,
		APIOrigin: "https://metadata.invalid", RetryAttempts: 1, DeviceResets: hgapi.Int(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	service.client = client
	service.seriesBatch = client.GetSeriesBatch
	service.series = client.GetSeries
	local := HistoryItem{
		SeriesID: id, Title: "第4集", EpisodeNum: 4, PositionSec: 91,
		TotalEpisodes: 8, UpdatedAt: time.Now().Add(time.Minute).Unix(),
	}
	if err := store.SaveHistory(local); err != nil {
		t.Fatal(err)
	}

	if err := service.syncRemoteHistory(context.Background(), service.currentHistoryGeneration()); err != nil {
		t.Fatalf("syncRemoteHistory() error = %v", err)
	}
	service.WaitForBackgroundSync()
	items, err := store.GetHistory()
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if len(items) != 1 || items[0].SeriesID != id {
		t.Fatalf("history rows = %+v, want one row for %s", items, id)
	}
	if items[0].Title != "远端短剧" || items[0].CoverURL != "https://image.invalid/cover.jpg" {
		t.Fatalf("history metadata = title %q, cover %q", items[0].Title, items[0].CoverURL)
	}
	if items[0].EpisodeNum != 4 || items[0].PositionSec != 91 || items[0].UpdatedAt != local.UpdatedAt {
		t.Fatalf("local playback clock changed: episode=%d position=%g updated=%d want updated=%d", items[0].EpisodeNum, items[0].PositionSec, items[0].UpdatedAt, local.UpdatedAt)
	}
}
