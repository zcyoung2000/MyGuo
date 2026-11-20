package media

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters"
	"myguo/internal/hgapi/adapters/memstore"
	"myguo/internal/hgapi/model"
)

func TestNextEpisodesKeepsOrderedPlayableWindow(t *testing.T) {
	episodes := []model.Episode{
		{ID: "3", Number: 3, Playable: true},
		{ID: "1", Number: 1, Playable: true},
		{ID: "4", Number: 4, Playable: true},
		{ID: "2b", Number: 2},
		{ID: "2", Number: 2, Playable: true},
		{ID: "2c", Number: 2, Playable: true},
	}
	got := NextEpisodes(episodes, 1)
	if len(got) != 2 || got[0].ID != "2" || got[1].ID != "3" {
		t.Fatalf("NextEpisodes = %+v, want episodes 2 and 3", got)
	}
	if len(NextEpisodes(episodes, 4)) != 0 {
		t.Fatal("window after the last episode should be empty")
	}
}

func TestMultiVideoModelBodyAndParser(t *testing.T) {
	episodes := []model.Episode{{ID: "7100000000000201"}, {ID: "7100000000000202"}}
	body, err := multiVideoModelBody(episodes)
	if err != nil {
		t.Fatal(err)
	}
	var request MultiVideoModelRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.MixedVideoIDMap["1004"]) != 2 || request.MixedVideoIDMap["1004"][0] != episodes[0].ID || request.BizParam["video_platform"] != float64(1024) || request.BizParam["device_level"] != float64(4) {
		t.Fatalf("request = %s", body)
	}
	if _, err := multiVideoModelBody(make([]model.Episode, 16)); err == nil {
		t.Fatal("oversized batch accepted")
	}

	response := []byte(`{"code":0,"data":{"7100000000000201":{"video_model":{"video_list":[{"main_url":"https://media.invalid/e1","video_meta":{"codec_type":"h264"}}]}},"7100000000000202":{"video_model":{"video_list":[{"main_url":"https://media.invalid/e2","video_meta":{"codec_type":"h264"}}]}}}}`)
	parsed, err := parseMultiVideoModels(response, episodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 2 || parsed[episodes[0].ID].EpisodeID != episodes[0].ID || len(parsed[episodes[1].ID].Streams) != 1 {
		t.Fatalf("parsed = %+v", parsed)
	}
	if _, err := parseMultiVideoModels([]byte(`{"code":100001}`), episodes); err == nil {
		t.Fatal("API error accepted")
	}
}

func TestMultiModelBatcherChunksDeduplicatesAndCancelsStaleWindow(t *testing.T) {
	requests := make([][]string, 0, 3)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request MultiVideoModelRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		requests = append(requests, request.MixedVideoIDMap["1004"])
		data := map[string]any{}
		for _, id := range request.MixedVideoIDMap["1004"] {
			data[id] = map[string]any{"video_model": map[string]any{"video_list": []any{map[string]any{"main_url": "https://media.invalid/" + id, "video_meta": map[string]any{"codec_type": "h264"}}}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer ts.Close()

	profiles := memstore.New()
	_ = profiles.Save(context.Background(), &hgapi.DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002"})
	client, err := adapters.NewClient("", hgapi.Options{ProfileStore: profiles, APIOrigin: ts.URL, DeviceResets: hgapi.Int(0)})
	if err != nil {
		t.Fatal(err)
	}
	batcher := NewMultiVideoModelBatcher(client, &http.Client{})
	batcher.multiVideoModelURL = ts.URL
	episodes := make([]model.Episode, 16)
	for i := range episodes {
		episodes[i].ID = strconv.Itoa(7100000000000201 + i)
	}
	batcher.ReplaceWindow(episodes)
	batcher.Run(context.Background(), func(string, *model.Playback) {})
	if len(requests) != 2 || len(requests[0]) != 15 || len(requests[1]) != 1 {
		t.Fatalf("batch requests = %v", requests)
	}

	secondRun := make(chan string, 1)
	go batcher.Run(context.Background(), func(string, *model.Playback) {})
	select {
	case id := <-secondRun:
		t.Fatalf("completed window repeated request and remembered %q", id)
	case <-time.After(50 * time.Millisecond):
	}

	batcher.ReplaceWindow([]model.Episode{{ID: "7100000000000301", Number: 1, Playable: true}})
	batcher.Run(context.Background(), func(string, *model.Playback) {})
	latest := requests[len(requests)-1:]
	if len(latest) != 1 || len(latest[0]) != 1 || latest[0][0] != "7100000000000301" {
		t.Fatalf("replacement requests = %v", requests)
	}
	batcher.mu.Lock()
	activeLoads := len(batcher.loads)
	batcher.mu.Unlock()
	if activeLoads != 0 {
		t.Fatalf("loads were not released: %d", activeLoads)
	}
	batcher.forgetWindow()
	batcher.mu.Lock()
	window := batcher.window
	batcher.mu.Unlock()
	if window != nil {
		t.Fatal("empty window was retained")
	}
}

func TestMultiModelBatcherFailureDoesNotRememberModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":100001,"message":"rate limited"}`))
	}))
	defer ts.Close()

	batcher := NewMultiVideoModelBatcher(&hgapi.Client{}, &http.Client{})
	batcher.multiVideoModelURL = ts.URL
	batcher.ReplaceWindow([]model.Episode{{ID: "e1"}})
	remembered := make(chan string, 3)
	go batcher.Run(context.Background(), func(id string, pb *model.Playback) {
		if pb != nil {
			remembered <- id
		}
	})
	select {
	case id := <-remembered:
		t.Fatalf("failed request remembered %q", id)
	default:
	}
}

func TestRememberedMultiModelUsesExistingCacheExpiry(t *testing.T) {
	ms := &MediaServer{pbCache: make(map[string]playbackCacheItem)}
	ms.RememberPlayback("episode", &model.Playback{Streams: []model.Stream{{URL: "https://media.invalid/a"}}})
	if _, err := ms.resolvePlayback(context.Background(), "episode"); err != nil {
		t.Fatalf("prefetched model did not hit cache: %v", err)
	}
	if ms.playbackCacheHits.Load() != 1 {
		t.Fatalf("cache hits = %d", ms.playbackCacheHits.Load())
	}
	ms.pbCacheMu.Lock()
	item := ms.pbCache["episode"]
	item.expiresAt = time.Now().Add(-time.Second)
	ms.pbCache["episode"] = item
	ms.pbCacheMu.Unlock()
	if _, err := ms.resolvePlayback(context.Background(), "episode"); !errors.Is(err, errNilPlaybackClient) {
		t.Fatalf("expired model was served: %v", err)
	}
}
