package media

import (
	"context"
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
	ms := &MediaServer{multiModel: NewMultiVideoModelBatcher(client, nil)}
	ms.multiModel.multiVideoModelURL = upstream.URL
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
