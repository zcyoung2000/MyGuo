package media

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters"
	"myguo/internal/hgapi/adapters/memstore"
	"myguo/internal/hgapi/model"
)

type multiVideoModelRequest struct {
	MixedVideoIDMap map[string][]string `json:"mixed_video_id_map"`
	DRScene         string              `json:"dr_scene"`
	BizParam        map[string]any      `json:"biz_param"`
}

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

func TestMultiModelBatcherChunksDeduplicatesAndCancelsStaleWindow(t *testing.T) {
	requests := make([][]string, 0, 3)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request multiVideoModelRequest
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
	batcher := NewMultiVideoModelBatcher(client)
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
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"code":100001,"message":"rate limited"}`))
	}))
	defer ts.Close()

	profiles := memstore.New()
	if err := profiles.Save(context.Background(), &hgapi.DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002"}); err != nil {
		t.Fatal(err)
	}
	client, err := adapters.NewClient("", hgapi.Options{ProfileStore: profiles, APIOrigin: ts.URL, DeviceResets: hgapi.Int(0)})
	if err != nil {
		t.Fatal(err)
	}
	batcher := NewMultiVideoModelBatcher(client)
	batcher.ReplaceWindow([]model.Episode{{ID: "7100000000000201"}})
	remembered := false
	batcher.Run(context.Background(), func(string, *model.Playback) { remembered = true })
	if requests.Load() == 0 || remembered {
		t.Fatalf("failed batch: requests=%d remembered=%t", requests.Load(), remembered)
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
