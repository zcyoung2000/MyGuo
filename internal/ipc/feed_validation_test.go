package ipc

import (
	"context"
	"errors"
	"testing"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/model"
)

func TestFeedValidatesBeforePersistenceAndReusesPlayableEpisodes(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const good, empty, removed, disabled = "710000000000001", "710000000000002", "710000000000003", "710000000000004"
	svc := &HongguoService{store: store, media: &MediaServer{},
		discover: func(context.Context, hgapi.DiscoverOptions) (*hgapi.Page[model.SeriesSummary], error) {
			return &hgapi.Page[model.SeriesSummary]{Items: []model.SeriesSummary{{ID: good, EpisodeCount: 99}, {ID: empty, EpisodeCount: 99}, {ID: removed}, {ID: disabled}}, Cursor: "next", HasMore: true}, nil
		},
		seriesBatch: func(context.Context, []string) ([]model.SeriesDetail, error) {
			return []model.SeriesDetail{
				{Series: &model.Series{SeriesSummary: model.SeriesSummary{ID: good, Title: "good", EpisodeCount: 2}}, Episodes: []model.Episode{{ID: "710000000000101", SeriesID: good, Number: 1, Playable: true}}},
				{Series: &model.Series{SeriesSummary: model.SeriesSummary{ID: empty, Title: "empty"}, RightText: "全0集"}},
				{Series: &model.Series{SeriesSummary: model.SeriesSummary{ID: disabled, Title: "disabled", EpisodeCount: 1}}, Episodes: []model.Episode{{ID: "710000000000401", Number: 1, Playable: false}}},
			}, nil
		},
	}
	page, err := svc.GetRecommendFeed(context.Background(), "推荐", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != good {
		t.Fatalf("unplayable cards admitted: %+v / %v", page, err)
	}
	queue, err := store.GetFeedQueue("推荐")
	if err != nil || len(queue) != 1 || queue[0].SeriesID != good {
		t.Fatalf("invalid persisted queue: %+v / %v", queue, err)
	}
	episodes, err := svc.listEpisodesCached(context.Background(), good)
	if err != nil || len(episodes.Items) != 1 || episodes.Items[0].ID != "710000000000101" {
		t.Fatalf("batch episodes not reused: %+v / %v", episodes, err)
	}
}

func TestRestoredQueueValidationRemovesOnlyRejectedEntries(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := &HongguoService{store: store, media: &MediaServer{}}
	if err := svc.queueRecommendCards("推荐", []SeriesCard{{ID: "good"}, {ID: "removed"}, {ID: "offline"}}); err != nil {
		t.Fatal(err)
	}
	svc.seriesBatch = func(_ context.Context, ids []string) ([]model.SeriesDetail, error) {
		if len(ids) > 1 || ids[0] == "removed" {
			return nil, hgapi.ErrContentUnavailable
		}
		if ids[0] == "offline" {
			return nil, errors.New("network unavailable")
		}
		return []model.SeriesDetail{{Series: &model.Series{SeriesSummary: model.SeriesSummary{ID: "good", EpisodeCount: 1}}, Episodes: []model.Episode{{ID: "episode", Number: 1, Playable: true}}}}, nil
	}
	page, err := svc.RestoreRecommendFeed(context.Background(), "推荐")
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != "good" || page.Items[1].ID != "offline" {
		t.Fatalf("restore invalid: %+v / %v", page, err)
	}
	queue, err := store.GetFeedQueue("推荐")
	if err != nil || len(queue) != 2 || queue[0].SeriesID != "good" || queue[1].SeriesID != "offline" {
		t.Fatalf("rejection consumed an earlier unviewed item: %+v / %v", queue, err)
	}
}
