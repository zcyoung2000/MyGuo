package uiapp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

type groupedRelatedProvider struct {
	*parityProvider
	page *ipc.SeriesCollectionDTO
}

func (p *groupedRelatedProvider) GetSeriesAdaptations(context.Context, string, string, string, string, string) (*ipc.SeriesCollectionDTO, error) {
	return p.page, nil
}

func TestOfficialRelatedPageRestoresOtherSeasonsAndCardNavigation(t *testing.T) {
	p := &groupedRelatedProvider{parityProvider: &parityProvider{}, page: &ipc.SeriesCollectionDTO{
		Title: "同一剧本改编", Items: []ipc.SeriesCard{{ID: "version", Title: "其他改编版本"}, {ID: "version", Title: "重复改编版本"}, {ID: "season-two", Title: "不得混入改编"}},
		Seasons: []ipc.SeriesCard{{ID: "current", Title: "当前版本"}, {ID: "season-two", Title: "续作第二季", EpisodeCount: 119, BadgeText: "第2季"}, {ID: "season-two", Title: "重复第二季"}, {ID: "season-three", Title: "续作第三季", Upcoming: true, BadgeText: "第3季"}},
	}}
	a := NewTestApp()
	a.featuresSource = p
	a.view = "detail"
	a.detailTab = detailTabRelated
	a.detail = Detail{ID: "current", Title: "当前剧", Seasons: DetailCollection{Loading: true}, Adaptations: DetailCollection{Loading: true}}
	a.loadSupplement("current", a.generation, true)
	d := a.DetailState()
	if d.Seasons.Loading || len(d.Seasons.Items) != 2 || len(d.Adaptations.Items) != 1 || !d.Seasons.Items[1].Upcoming {
		t.Fatalf("related groups incorrect: seasons=%+v adaptations=%+v", d.Seasons, d.Adaptations)
	}
	for _, call := range p.calls {
		if call == "seasons:current" {
			t.Fatal("extra season request")
		}
	}
	tt := scrollInputTester(a, 1200, 1200)
	tt.Frame()
	season, ok := tt.Find("同系列其他季")
	if !ok {
		t.Fatal("season group absent")
	}
	adaptation, ok := tt.Find("同系列作品")
	if !ok || adaptation.Y >= season.Y {
		t.Fatal("groups missing or mixed")
	}
	if tt.HasText("重复第二季") || tt.HasText("不得混入改编") || tt.HasText("猜你喜欢") {
		t.Fatal("wrong related items displayed")
	}
	assertQuietLoading(t, tt)
	if _, ok := tt.AccessByRole(ui.RoleButton, "续作第二季"); !ok {
		t.Fatal("season card is not actionable")
	}
	if err := tt.Click("续作第二季"); err != nil {
		t.Fatal(err)
	}
	waitFor(time.Second, func() bool { return a.DetailState().ID == "season-two" })
	if a.DetailState().ID != "season-two" {
		t.Fatal("season card did not navigate to its own detail")
	}
}

func TestOnlyOtherSeasonsDoesNotRequireAdaptations(t *testing.T) {
	p := &groupedRelatedProvider{page: &ipc.SeriesCollectionDTO{Seasons: []ipc.SeriesCard{{ID: "season", Title: "独立续季"}}}}
	a := NewTestApp()
	a.featuresSource = p
	a.view = "detail"
	a.detailTab = detailTabRelated
	a.detail = Detail{ID: "current"}
	a.loadSupplement("current", a.generation, true)
	tt := scrollInputTester(a, 1000, 1000)
	if !tt.HasText("独立续季") || tt.HasText("暂无相关作品") || tt.HasText("同系列作品") {
		t.Fatal("season-only response hidden or misleading")
	}
}

type pagedGroupedProvider struct {
	*groupedRelatedProvider
	next *ipc.SeriesCollectionDTO
}

func (p *pagedGroupedProvider) GetSeriesAdaptationsPage(context.Context, string, string, string, string, string, string) (*ipc.SeriesCollectionDTO, error) {
	return p.next, nil
}
func TestSeasonOnlyContinuationKeepsBothGroupsAndDoesNotCountAsEmpty(t *testing.T) {
	p := &pagedGroupedProvider{groupedRelatedProvider: &groupedRelatedProvider{page: &ipc.SeriesCollectionDTO{HasMore: true, Cursor: "1", Items: []ipc.SeriesCard{{ID: "version", Title: "其他版本"}}, Seasons: []ipc.SeriesCard{{ID: "s1", Title: "第一季"}}}}}
	a := NewTestApp()
	a.featuresSource = p
	a.detail = Detail{ID: "current"}
	a.loadSupplement("current", a.generation, true)
	for i := 2; i <= 5; i++ {
		p.next = &ipc.SeriesCollectionDTO{HasMore: true, Cursor: fmt.Sprint(i), Seasons: []ipc.SeriesCard{{ID: "s1", Title: "重复第一季"}, {ID: fmt.Sprint(i), Title: fmt.Sprintf("第%d季", i)}}}
		a.loadSupplement("current", a.generation, true, true)
		d := a.DetailState()
		if len(d.Seasons.Items) != i || len(d.Adaptations.Items) != 1 || d.Adaptations.Error != "" {
			t.Fatalf("season continuation lost data or appeared empty: %+v", d)
		}
	}
}
