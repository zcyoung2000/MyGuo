package media

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters"
	"myguo/internal/hgapi/adapters/memstore"
	"myguo/internal/hgapi/model"
)

func TestModelLookaheadReplacesCanceledFlightBeforeCleanup(t *testing.T) {
	requests := make(chan []string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request multiVideoModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		ids := request.MixedVideoIDMap["1004"]
		requests <- ids
		data := map[string]any{}
		for _, id := range ids {
			data[id] = map[string]any{"video_model": map[string]any{"video_list": []any{map[string]any{"main_url": "https://media.invalid/" + id, "video_meta": map[string]any{"codec_type": "h264"}}}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer upstream.Close()
	profiles := memstore.New()
	if err := profiles.Save(context.Background(), &hgapi.DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002"}); err != nil {
		t.Fatal(err)
	}
	client, err := adapters.NewClient("", hgapi.Options{ProfileStore: profiles, APIOrigin: upstream.URL, RetryAttempts: 1, DeviceResets: hgapi.Int(0)})
	if err != nil {
		t.Fatal(err)
	}
	owner, cancel := context.WithCancel(context.Background())
	cancel()
	old := &playbackLoad{done: make(chan struct{}), owner: owner}
	episodes := []model.Episode{{ID: "7100000000000201"}, {ID: "7100000000000202"}}
	ms := &MediaServer{multiModel: NewMultiVideoModelBatcher(client), pbLoads: map[string]*playbackLoad{episodes[0].ID: old}}
	ms.loadPlaybackModels(context.Background(), episodes)
	select {
	case ids := <-requests:
		if len(ids) != 2 || ids[0] != episodes[0].ID || ids[1] != episodes[1].ID {
			t.Fatalf("cancelled flight excluded an overlapping episode from the new batch: %v", ids)
		}
	default:
		t.Fatal("replacement model window did not fetch its episodes")
	}
	// A late cancelled batch must not remove models published by its successor.
	ms.finishPlaybackLoad(episodes[0].ID, old, nil, context.Canceled)
	if pb, err := ms.resolvePlayback(context.Background(), episodes[0].ID); pb == nil || err != nil {
		t.Fatalf("old batch cleanup damaged the replacement model: %v/%v", pb, err)
	}
}

func TestModelLookaheadSharesFailedFlightCacheAndCooldown(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var batchCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if batchCalls.Add(1) == 1 {
			close(started)
			<-release
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()
	profiles := memstore.New()
	if err := profiles.Save(context.Background(), &hgapi.DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002"}); err != nil {
		t.Fatal(err)
	}
	client, err := adapters.NewClient("", hgapi.Options{ProfileStore: profiles, APIOrigin: upstream.URL, RegisterOrigin: upstream.URL, RetryAttempts: 1, DeviceResets: hgapi.Int(0)})
	if err != nil {
		t.Fatal(err)
	}
	ms := &MediaServer{multiModel: NewMultiVideoModelBatcher(client)}
	ms.multiModel.loadModels = ms.loadPlaybackModels
	var foregroundCalls atomic.Int32
	ms.playbackResolver = fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
		foregroundCalls.Add(1)
		return playbackResult("unexpected"), nil
	}}
	episodes := []model.Episode{{ID: "7100000000000201"}, {ID: "7100000000000202"}}
	batchDone := make(chan struct{})
	go func() { ms.loadPlaybackModels(context.Background(), episodes); close(batchDone) }()
	<-started
	foregroundDone := make(chan error, 1)
	go func() { _, err := ms.resolvePlayback(context.Background(), episodes[0].ID); foregroundDone <- err }()
	close(release)
	<-batchDone
	if err := <-foregroundDone; !errors.Is(err, hgapi.ErrRateLimited) {
		t.Fatalf("foreground did not share batch failure: %v", err)
	}
	if batchCalls.Load() != 1 || foregroundCalls.Load() != 0 || ms.pbConsecutiveFailures != 2 {
		t.Fatalf("batch/foreground/streak = %d/%d/%d", batchCalls.Load(), foregroundCalls.Load(), ms.pbConsecutiveFailures)
	}
	ms.loadPlaybackModels(context.Background(), episodes)
	if batchCalls.Load() != 1 || ms.pbConsecutiveFailures != 2 {
		t.Fatal("lookahead bypassed its failure cache")
	}
	ms.pbCooldownUntil = time.Now().Add(time.Minute)
	ms.multiModel.ReplaceWindow([]model.Episode{{ID: "7100000000000203"}})
	ms.multiModel.Run(context.Background(), func(string, *model.Playback) { t.Fatal("server-owned model publication used standalone callback") })
	if batchCalls.Load() != 1 || len(ms.pbLoads) != 0 {
		t.Fatal("lookahead bypassed global cooldown")
	}
	ms.pbCooldownUntil = time.Time{}
	for _, episode := range episodes {
		expirePlaybackFailure(ms, episode.ID)
	}
	ms.loadPlaybackModels(context.Background(), episodes)
	if batchCalls.Load() != 2 || ms.pbConsecutiveFailures != 4 {
		t.Fatalf("lookahead did not reload expired failure: %d/%d", batchCalls.Load(), ms.pbConsecutiveFailures)
	}
}
