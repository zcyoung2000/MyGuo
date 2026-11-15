package uiapp

import (
	"context"
	"github.com/egoist/mygo/ui"
	"sync/atomic"
	"testing"

	"myguo/internal/ipc"
)

type relatedDetailProvider struct{ guesses atomic.Int32 }

func (*relatedDetailProvider) GetSeriesPage(context.Context, string) (*ipc.SeriesDetailPageDTO, error) {
	return &ipc.SeriesDetailPageDTO{SeriesDetailDTO: ipc.SeriesDetailDTO{ID: "123456", Title: "详情页"}}, nil
}

func (p *relatedDetailProvider) GetSeriesRelated(context.Context, string) (*ipc.SeriesCollectionDTO, error) {
	p.guesses.Add(1)
	return &ipc.SeriesCollectionDTO{Title: "相关推荐", Items: []ipc.SeriesCard{{
		ID: "related-1", Title: "相关剧", Category: "漫剧", BadgeText: "置顶推荐",
		EpisodeCount: 1000, Heat: "9839.2万热度",
	}}}, nil
}

func (*relatedDetailProvider) GetFilterPanel(context.Context, string) ([]ipc.FilterRowDTO, error) {
	return nil, nil
}

func (*relatedDetailProvider) GetDiscover(context.Context, string, map[string]string, string) (ipc.DiscoverResponse, error) {
	return ipc.DiscoverResponse{}, nil
}

func TestDetailDoesNotRequestGuessRecommendations(t *testing.T) {
	app := NewTestApp()
	provider := &relatedDetailProvider{}
	app.detailSource = provider
	app.view = "detail"
	app.loadDetailAt("123456", app.generation)
	if provider.guesses.Load() != 0 {
		t.Fatal("detail requested unwanted recommendations")
	}
	app.detailTab = detailTabRelated
	tt := ui.NewTester(app.Render, 1200, 800)
	if tt.HasText("猜你喜欢") || tt.HasText("相关剧") {
		t.Fatal("unwanted recommendations rendered")
	}
}
