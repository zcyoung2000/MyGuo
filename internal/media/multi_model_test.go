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

func TestClearingReplacedMultiModelWindowDoesNotBlockPlayback(t *testing.T) {
	batcher := NewMultiVideoModelBatcher(&hgapi.Client{})
	requests := make(chan string, 2)
	cancelled := make(chan struct{})
	releaseCtx, release := context.WithCancel(context.Background())
	defer release()
	batcher.loadModels = func(ctx context.Context, episodes []model.Episode) {
		requests <- episodes[0].ID
		if episodes[0].ID == "old" {
			<-ctx.Done()
			close(cancelled)
			// Keep cancellation cleanup pending while another selection clears
			// the window, as a cached episode without lookahead does.
			<-releaseCtx.Done()
		}
	}
	batcher.ReplaceWindow([]model.Episode{{ID: "old"}})
	runDone := make(chan struct{})
	go func() {
		batcher.Run(context.Background(), nil)
		close(runDone)
	}()
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("initial model prefetch did not start")
	}
	batcher.ReplaceWindow([]model.Episode{{ID: "new"}})
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("replacing the window did not cancel old prefetch")
	}
	cleared := make(chan struct{})
	go func() {
		batcher.forgetWindow()
		close(cleared)
	}()
	select {
	case <-cleared:
	case <-time.After(time.Second):
		t.Fatal("clearing an already-cancelled prefetch blocked subsequent playback")
	}
	release()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled model prefetch could not release its loads")
	}
	batcher.ReplaceWindow([]model.Episode{{ID: "new"}})
	batcher.Run(context.Background(), nil)
	select {
	case id := <-requests:
		if id != "new" {
			t.Fatalf("next prefetch requested %q, want new episode", id)
		}
	default:
		t.Fatal("new playback could not prefetch after clearing the old window")
	}
}

func TestStaleMultiModelTaskCannotRestartAfterWindowIsCleared(t *testing.T) {
	batcher := NewMultiVideoModelBatcher(&hgapi.Client{})
	batcher.loadModels = func(context.Context, []model.Episode) {
		t.Error("stale task started a model request after its window was cleared")
	}
	batcher.ReplaceWindow([]model.Episode{{ID: "old"}})
	windowID, tasks, _ := batcher.snapshotWindow()
	batcher.forgetWindow()
	// A worker can be scheduled between snapshotting a window and registering
	// its loads. That registration must not resurrect the cancelled window.
	batcher.runTask(context.Background(), tasks[0], nil, windowID)
	if len(batcher.loads) != 0 {
		t.Fatal("stale task retained loads after its window was cleared")
	}
}

func TestReplacingMultiModelWindowStartsOverlappingEpisodesBeforeOldCleanup(t *testing.T) {
	batcher := NewMultiVideoModelBatcher(&hgapi.Client{})
	requests := make(chan []model.Episode, 2)
	oldRelease, releaseOld := context.WithCancel(context.Background())
	defer releaseOld()
	newRelease, releaseNew := context.WithCancel(context.Background())
	defer releaseNew()
	newContext := make(chan context.Context, 1)
	batcher.loadModels = func(ctx context.Context, episodes []model.Episode) {
		requests <- episodes
		if episodes[0].ID == "A" {
			<-ctx.Done()
			<-oldRelease.Done()
		} else {
			newContext <- ctx
			<-newRelease.Done()
		}
	}
	batcher.ReplaceWindow([]model.Episode{{ID: "A"}, {ID: "B"}})
	oldDone := make(chan struct{})
	go func() { batcher.Run(context.Background(), nil); close(oldDone) }()
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("old window did not start")
	}
	batcher.ReplaceWindow([]model.Episode{{ID: "B"}, {ID: "C"}})
	newDone := make(chan struct{})
	go func() { batcher.Run(context.Background(), nil); close(newDone) }()
	select {
	case episodes := <-requests:
		if len(episodes) != 2 || episodes[0].ID != "B" || episodes[1].ID != "C" {
			t.Fatalf("replacement requested the wrong episodes: %v", episodes)
		}
	case <-time.After(time.Second):
		t.Fatal("overlap with cancelled loads silently discarded the replacement window")
	}
	ctx := <-newContext
	releaseOld()
	select {
	case <-oldDone:
	case <-time.After(time.Second):
		t.Fatal("old window did not finish cleanup")
	}
	batcher.mu.Lock()
	retained := batcher.loads["B"] != nil && batcher.loads["C"] != nil
	batcher.mu.Unlock()
	if !retained || ctx.Err() != nil {
		t.Fatal("old cleanup cancelled or removed the replacement window's loads")
	}
	releaseNew()
	select {
	case <-newDone:
	case <-time.After(time.Second):
		t.Fatal("replacement window did not finish")
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
