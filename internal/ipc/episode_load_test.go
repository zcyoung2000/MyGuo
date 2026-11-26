package ipc

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters"
	"myguo/internal/hgapi/adapters/memstore"
	"myguo/internal/hgapi/model"
)

type episodeWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *episodeWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestActiveEpisodeWaiterRecoversFromCanceledDetailOwner(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"code":0,"data":{"video_data":{"series_id_str":"710000000000001","series_title":"测试短剧","episode_cnt":0,"video_list":[]}}}`))
			}))
			defer server.Close()
			profiles := memstore.New()
			if err := profiles.Save(context.Background(), &hgapi.DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002"}); err != nil {
				t.Fatal(err)
			}
			client, err := adapters.NewClient("", hgapi.Options{ProfileStore: profiles, APIOrigin: server.URL, RetryAttempts: 1, DeviceResets: hgapi.Int(0)})
			if err != nil {
				t.Fatal(err)
			}
			load := &episodeLoad{done: make(chan struct{})}
			svc := &HongguoService{
				client:       client,
				episodeLoads: map[string]*episodeLoad{"710000000000001": load},
				seriesBatch: func(context.Context, []string) ([]model.SeriesDetail, error) {
					return []model.SeriesDetail{{
						Series:   &model.Series{SeriesSummary: model.SeriesSummary{ID: "710000000000001", EpisodeCount: 1}},
						Episodes: []model.Episode{{ID: "710000000000101", Number: 1, Playable: true}},
					}}, nil
				},
			}
			base, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			ctx := &episodeWaitContext{Context: base, waiting: make(chan struct{})}
			result := make(chan episodeListResult, 1)
			go func() {
				page, err := svc.listEpisodesCached(ctx, "710000000000001")
				result <- episodeListResult{page: page, err: err}
			}()
			select {
			case <-ctx.waiting:
			case <-base.Done():
				t.Fatal("foreground did not join the detail load")
			}
			svc.episodeMu.Lock()
			delete(svc.episodeLoads, "710000000000001")
			load.result.err = cause
			close(load.done)
			svc.episodeMu.Unlock()
			select {
			case got := <-result:
				if got.err != nil || len(pageItems(got.page)) != 1 || got.page.Items[0].ID != "710000000000101" {
					t.Fatalf("foreground episode resolution inherited cancelled detail: %v/%v", got.page, got.err)
				}
			case <-base.Done():
				t.Fatal("foreground did not recover after the detail owner stopped")
			}
		})
	}
}

func TestEpisodeLoadBroadcastsToAllPlaybackAndDetailWaiters(t *testing.T) {
	for _, wantErr := range []error{nil, errors.New("unavailable")} {
		load := &episodeLoad{done: make(chan struct{})}
		svc := &HongguoService{episodeLoads: map[string]*episodeLoad{"series": load}}
		want := &hgapi.Page[model.Episode]{Items: []model.Episode{{ID: "episode", Number: 1}}}
		var waiters sync.WaitGroup
		for i := 0; i < 32; i++ {
			waiters.Add(1)
			go func() {
				defer waiters.Done()
				page, err := svc.listEpisodesCached(context.Background(), "series")
				if page != want || err != wantErr {
					t.Errorf("shared episodes = %p/%v, want %p/%v", page, err, want, wantErr)
				}
			}()
		}
		load.result = episodeListResult{page: want, err: wantErr}
		close(load.done)
		waiters.Wait()
	}
}

func TestEpisodeCacheEvictsOldestAtLimit(t *testing.T) {
	service := &HongguoService{
		episodeCache:      make(map[string]episodeCacheEntry),
		episodeCacheIndex: make(map[string]*list.Element),
		episodeCacheLRU:   list.New(),
	}
	for seriesNumber := 0; seriesNumber < 129; seriesNumber++ {
		id := fmt.Sprintf("series-%03d", seriesNumber)
		service.rememberEpisode(id, &hgapi.Page[model.Episode]{Items: []model.Episode{{ID: id}}}, time.Now())
	}

	if len(service.episodeCache) != 128 {
		t.Fatalf("episode cache size = %d, want 128", len(service.episodeCache))
	}
	if _, ok := service.episodeCache["series-000"]; ok {
		t.Fatal("oldest episode cache entry was not evicted")
	}
	if _, ok := service.episodeCache["series-128"]; !ok {
		t.Fatal("newest episode cache entry was not retained")
	}
	if _, ok := service.episodeCacheIndex["series-128"]; !ok {
		t.Fatal("newest episode cache LRU index entry was not retained")
	}
}

func TestEpisodeCacheExpiresStaleEntry(t *testing.T) {
	service := &HongguoService{
		episodeCache: map[string]episodeCacheEntry{
			"series": {page: &hgapi.Page[model.Episode]{Items: []model.Episode{{ID: "stale"}}}, expiresAt: time.Now().Add(-time.Second)},
		},
		episodeCacheIndex: make(map[string]*list.Element),
		episodeCacheLRU:   list.New(),
	}

	service.forgetExpiredEpisodes(time.Now())

	if len(service.episodeCache) != 0 {
		t.Fatalf("stale episode cache entries = %d, want 0", len(service.episodeCache))
	}
	if service.episodeCacheLRU.Len() != 0 {
		t.Fatalf("stale episode cache LRU entries = %d, want 0", service.episodeCacheLRU.Len())
	}
	if len(service.episodeCacheIndex) != 0 {
		t.Fatalf("stale episode cache index entries = %d, want 0", len(service.episodeCacheIndex))
	}
}

func TestPlaybackEpisodeWaitCanTimeoutWithoutConsumingSharedResult(t *testing.T) {
	load := &episodeLoad{done: make(chan struct{})}
	svc := &HongguoService{episodeLoads: map[string]*episodeLoad{"series": load}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := svc.listEpisodesCached(ctx, "series")
		finished <- err
	}()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("canceled playback waiter = %v", err)
		}
	case <-time.After(time.Second):
		close(load.done)
		t.Fatal("playback waited past its own deadline for a background detail load")
	}
	load.result.page = &hgapi.Page[model.Episode]{Items: []model.Episode{{ID: "episode"}}}
	close(load.done)
	if page, err := svc.listEpisodesCached(context.Background(), "series"); page != load.result.page || err != nil {
		t.Fatalf("later detail waiter lost shared result: %p/%v", page, err)
	}
}

func TestPlaybackPreservesEpisodeErrorWithoutSecondRequest(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, cause := range []error{errors.New("upstream unavailable"), hgapi.ErrAnonymousLimited} {
		load := &episodeLoad{done: make(chan struct{})}
		load.result.err = cause
		close(load.done)
		svc := &HongguoService{store: store, media: &MediaServer{}, episodeLoads: map[string]*episodeLoad{"series": load}}
		_, err := svc.PlayEpisode(context.Background(), "series", "", 1, "720p", true)
		if !errors.Is(err, cause) {
			t.Fatalf("lost cause: %v", err)
		}
	}
}
