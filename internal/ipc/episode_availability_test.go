package ipc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters"
	"myguo/internal/hgapi/adapters/memstore"
	"myguo/internal/hgapi/model"
)

func TestEmptySingleDetailIsVerifiedWithoutChangingDevice(t *testing.T) {
	const id = "710000000000001"
	for _, scenario := range []string{"zero-episodes", "batch-recovery", "inconclusive-detail"} {
		t.Run(scenario, func(t *testing.T) {
			var registered, batches atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "device_register") {
					registered.Add(1)
					_, _ = w.Write([]byte(`{"device_id":123456789,"install_id":987654321,"device_id_str":"123456789","install_id_str":"987654321","new_user":1}`))
					return
				}
				_, _ = w.Write([]byte(`{"code":0,"data":{"video_data":{"series_id_str":"710000000000001","series_title":"零集作品","episode_cnt":0,"video_list":[]}}}`))
			}))
			defer server.Close()
			client, err := adapters.NewClient("", hgapi.Options{ProfileStore: memstore.New(), APIOrigin: server.URL, RegisterOrigin: server.URL, RetryAttempts: 1})
			if err != nil {
				t.Fatal(err)
			}
			service := &HongguoService{client: client, seriesBatch: func(ctx context.Context, ids []string) ([]model.SeriesDetail, error) {
				batches.Add(1)
				if len(ids) != 1 || ids[0] != id {
					t.Errorf("wrong batch scope: %v", ids)
				}
				d := model.SeriesDetail{Series: &model.Series{SeriesSummary: model.SeriesSummary{ID: id}, RightText: "全0集"}}
				if scenario == "inconclusive-detail" {
					d.Series.RightText = ""
				}
				if scenario == "batch-recovery" {
					d.Series.EpisodeCount = 1
					d.Episodes = []model.Episode{{ID: "710000000000101", SeriesID: id, Number: 1, Playable: true}}
				}
				return []model.SeriesDetail{d}, nil
			}}
			page, err := service.listEpisodesCached(context.Background(), id)
			if scenario == "batch-recovery" {
				if err != nil || len(pageItems(page)) != 1 {
					t.Fatalf("batch recovery failed: %v/%v", page, err)
				}
			} else if scenario == "inconclusive-detail" {
				if !errors.Is(err, hgapi.ErrAnonymousLimited) {
					t.Fatalf("inconclusive metadata marked unavailable: %v", err)
				}
			} else if !errors.Is(err, ErrNoPlayableEpisodes) {
				t.Fatalf("zero episodes misclassified: %v", err)
			}
			if registered.Load() != 1 || batches.Load() != 1 {
				t.Fatalf("unnecessary device reset or repeated batch: registered=%d batch=%d", registered.Load(), batches.Load())
			}
		})
	}
}

func TestZeroEpisodeMetadataMarksRecommendationUnavailable(t *testing.T) {
	card := seriesCardFromDetail(SeriesCard{ID: "book", EpisodeCount: 99}, &model.Series{SeriesSummary: model.SeriesSummary{ID: "book"}, RightText: "全0集"}, func(s string) string { return s })
	if !card.Upcoming || card.EpisodeCount != 0 {
		t.Fatalf("zero-episode work remained playable: %+v", card)
	}
}

func TestIncompleteDetailDoesNotSuppressRecommendation(t *testing.T) {
	for _, count := range []int{0, 99} {
		for _, rightText := range []string{"", "已完结"} {
			card := seriesCardFromDetail(SeriesCard{ID: "book", EpisodeCount: count}, &model.Series{SeriesSummary: model.SeriesSummary{ID: "book"}, RightText: rightText}, func(s string) string { return s })
			if card.Upcoming || card.EpisodeCount != count {
				t.Fatalf("incomplete detail suppressed recommendation: %+v", card)
			}
		}
	}
}
