package uiapp

import (
	"context"
	"errors"
	"myguo/internal/ipc"
	"sync/atomic"
	"testing"
	"time"
)

type pagedAdaptationsProvider struct {
	*parityProvider
	fail   atomic.Bool
	called chan string
}

func (p *pagedAdaptationsProvider) GetSeriesAdaptationsPage(_ context.Context, _, _, _, _, _, cursor string) (*ipc.SeriesCollectionDTO, error) {
	p.called <- cursor
	if p.fail.Load() {
		return nil, errors.New("temporary upstream failure")
	}
	return &ipc.SeriesCollectionDTO{Title: "同一剧本改编", Items: []ipc.SeriesCard{{ID: "first", Title: "改编甲"}, {ID: "second", Title: "改编乙"}, {ID: "season", Title: "续季"}}}, nil
}
func TestAdaptationPagingAppendsDeduplicatesAndCanRetry(t *testing.T) {
	p := &pagedAdaptationsProvider{parityProvider: &parityProvider{adaptations: &ipc.SeriesCollectionDTO{Title: "同一剧本改编", Items: []ipc.SeriesCard{{ID: "current", Title: "当前剧"}, {ID: "season", Title: "续季"}, {ID: "first", Title: "改编甲"}}, HasMore: true, Cursor: "next"}}, called: make(chan string, 2)}
	a := NewTestApp()
	a.featuresSource = p
	a.view = "detail"
	a.detailTab = detailTabRelated
	a.detail = Detail{ID: "current", Title: "当前剧", Seasons: DetailCollection{Items: []CatalogItem{{ID: "season", Title: "续季"}}}}
	a.loadSupplement("current", a.generation, true)
	if len(a.detail.Adaptations.Items) != 1 {
		t.Fatal("current or seasons leaked into adaptations")
	}
	p.fail.Store(true)
	tt := scrollInputTester(a, 1200, 1000)
	tt.Frame()
	assertQuietLoading(t, tt)
	if cursor := <-p.called; cursor != "next" {
		t.Fatal("wrong continuation")
	}
	waitFor(time.Second, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.detail.Adaptations.Error != "" })
	state := a.DetailState().Adaptations
	if len(state.Items) != 1 || !state.HasMore || state.LoadingMore {
		t.Fatal("failed continuation lost existing results")
	}
	p.fail.Store(false)
	tt.Frame()
	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	<-p.called
	waitFor(time.Second, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return len(a.detail.Adaptations.Items) == 2 })
	tt.Frame()
	state = a.DetailState().Adaptations
	if state.HasMore || state.Error != "" || !tt.HasText("改编乙") || !tt.HasText("续季") {
		t.Fatalf("paging/filter failed: %+v", state)
	}
}
