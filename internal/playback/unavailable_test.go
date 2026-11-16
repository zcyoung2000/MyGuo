package playback

import (
	"context"
	"errors"
	"strings"
	"testing"

	"myguo/internal/ipc"
)

func TestRestoredUnavailableRecommendationIsConsumedAndSkipped(t *testing.T) {
	b := &testBackend{restore: &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "empty"}, {ID: "good"}}}, feed: &ipc.RecommendFeedResult{}}
	b.resolve = func(_ context.Context, id string, n int, q string) (*ipc.PlaybackDTO, error) {
		if id == "empty" {
			return nil, ipc.ErrNoPlayableEpisodes
		}
		return source(id, n, q), nil
	}
	c, _ := newControllerTest(t, b)
	c.ShowFeed("")
	waitSource(t, c, "good", 1)
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, v := range b.consumed {
			if strings.HasSuffix(v, ":empty") {
				return true
			}
		}
		return false
	})
	if s := c.Snapshot(); s.Error != "" || s.Book.ID != "good" {
		t.Fatalf("feed remained blocked: %+v", s)
	}
	c.Command("prev", nil)
	barrier(t, c)
	if c.Snapshot().Book.ID == "empty" {
		t.Fatal("unavailable work remained in reverse navigation")
	}
}

func TestExplicitUnavailableSeriesShowsAccurateError(t *testing.T) {
	b := &testBackend{resolve: func(context.Context, string, int, string) (*ipc.PlaybackDTO, error) {
		return nil, ipc.ErrNoPlayableEpisodes
	}}
	c, _ := newControllerTest(t, b)
	c.Play("empty", 1, 0, 1)
	eventually(t, func() bool { return c.Snapshot().Error != "" })
	if s := c.Snapshot(); !strings.Contains(s.Error, "暂未提供可播放剧集") || s.Loading {
		t.Fatalf("wrong unavailable state: %+v", s)
	}
}

func TestNetworkFailureDoesNotSkipRecommendation(t *testing.T) {
	b := &testBackend{restore: &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "offline"}, {ID: "good"}}}, feed: &ipc.RecommendFeedResult{}}
	b.resolve = func(_ context.Context, id string, n int, q string) (*ipc.PlaybackDTO, error) {
		if id == "offline" {
			return nil, errors.New("network unavailable")
		}
		return source(id, n, q), nil
	}
	c, _ := newControllerTest(t, b)
	c.ShowFeed("")
	eventually(t, func() bool { return c.Snapshot().Error != "" })
	if c.Snapshot().Book.ID != "offline" {
		t.Fatal("transport failure silently skipped requested work")
	}
}

func TestUnavailableTailWaitsForNextPageWithoutReplayingOlderWork(t *testing.T) {
	for _, scenario := range []string{"next-page", "filtered-page", "exhausted"} {
		t.Run(scenario, func(t *testing.T) {
			release := make(chan struct{})
			b := &testBackend{restore: &ipc.RecommendFeedResult{
				Items: []ipc.SeriesCard{{ID: "older"}, {ID: "empty"}}, HasMore: true, Cursor: "first",
			}}
			b.resolve = func(_ context.Context, id string, n int, q string) (*ipc.PlaybackDTO, error) {
				if id == "empty" {
					return nil, ipc.ErrNoPlayableEpisodes
				}
				return source(id, n, q), nil
			}
			b.fetch = func(ctx context.Context, _, cursor string) (*ipc.RecommendFeedResult, error) {
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				if scenario == "exhausted" {
					return &ipc.RecommendFeedResult{}, nil
				}
				if scenario == "filtered-page" && cursor == "first" {
					return &ipc.RecommendFeedResult{HasMore: true, Cursor: "next"}, nil
				}
				return &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "newer"}}}, nil
			}
			c, _ := newControllerTest(t, b)
			c.ShowFeed("")
			waitSource(t, c, "older", 1)
			c.Command("next", nil)
			eventually(t, func() bool {
				s := c.Snapshot()
				return s.Book.ID == "empty" && s.Engine == nil && s.Loading
			})
			close(release)
			if scenario == "exhausted" {
				eventually(t, func() bool { s := c.Snapshot(); return !s.Loading && s.Error != "" && s.Engine == nil })
			} else {
				waitSource(t, c, "newer", 1)
			}
		})
	}
}
