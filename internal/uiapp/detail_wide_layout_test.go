package uiapp

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"myguo/internal/ipc"
)

func detailWideFixture() Detail {
	episode := func(number int) CatalogItem {
		return CatalogItem{ID: "123456", EpisodeID: "episode", EpisodeCount: number}
	}
	episodes := make([]CatalogItem, 0, 100)
	for number := 1; number <= 100; number++ {
		episodes = append(episodes, episode(number))
	}
	return Detail{
		ID: "123456", Title: "宽屏详情", Description: "双栏布局验证",
		Source:      &ipc.SourceNovelDTO{ID: "source", Title: "原著小说"},
		Actors:      []ipc.ActorInfo{{Name: "演员甲", UserID: "actor-id"}, {Name: "演员乙"}},
		Episodes:    episodes,
		Seasons:     DetailCollection{Title: "同系列短剧", Items: []CatalogItem{{ID: "season", Title: "同系列短剧"}}},
		Adaptations: DetailCollection{Title: "同一剧本改编", Items: []CatalogItem{{ID: "adaptation", Title: "同一剧本改编"}}},
	}
}

func emptyDetailFixture() Detail {
	fixture := detailWideFixture()
	fixture.Seasons = DetailCollection{}
	fixture.Adaptations = DetailCollection{}
	return fixture
}

func TestDetailFullWidthEpisodesBelowHero(t *testing.T) {
	for _, width := range []int{800, 1000, 1600} {
		app := NewTestApp()
		app.view = "detail"
		app.detail = detailWideFixture()
		tt := renderTesterAndAdvance(t, app, width, 1000)
		hero, ok := tt.Find("宽屏详情")
		if !ok {
			t.Fatal("title missing")
		}
		tabs, ok := tt.Find("剧集内容")
		if !ok {
			t.Fatal("native tabs missing")
		}
		first, ok := tt.Find("第1集")
		if !ok {
			t.Fatal("first episode missing")
		}
		if tabs.Y < hero.Y+hero.H || first.Y < tabs.Y+tabs.H {
			t.Fatalf("content order wrong: hero=%v tabs=%v first=%v", hero, tabs, first)
		}
		if first.X >= hero.X || first.W < 126 || first.H != 46 {
			t.Fatalf("episodes do not use full content width: hero=%v first=%v", hero, first)
		}
		if !tt.HasText("原著小说") {
			t.Fatal("source summary missing from introduction")
		}
	}
}

func TestDetailOnePageScrollPinsTabs(t *testing.T) {
	app := NewTestApp()
	app.view = "detail"
	app.detail = longDetailEpisodesFixture(812)
	tt := scrollInputTester(app, 1600, 1000)
	tt.Frame()
	first, _ := tt.Find("第1集")
	before, _ := tt.Find("返回上一页")
	tt.Scroll(first.X+20, first.Y+20, 0, 650)
	tt.Frame()
	tabs, ok := tt.Find("剧集内容")
	list, _ := tt.Find("剧集详情列表")
	if app.detailScroll.Y <= 0 || !ok || absFloat(tabs.Y-list.Y) > 1 {
		t.Fatalf("tabs not pinned: y=%v tabs=%v list=%v", app.detailScroll.Y, tabs, list)
	}
	after, _ := tt.Find("返回上一页")
	if before != after {
		t.Fatal("back moved with page")
	}
	if tt.HasText("宽屏详情") {
		t.Fatal("hero did not scroll with episodes")
	}
	if err := tt.Click("相关作品"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("同一剧本改编") {
		t.Fatalf("related section unreachable after scrolling: %v", tt.Texts())
	}
	if err := tt.Click("选集 812"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	app.detailEpisodesList.ScrollToEnd()
	tt.Frame()
	if !tt.HasText("第812集") {
		t.Fatal("episodes lost after tab round trip")
	}
}

func TestDetailEmptyCollectionsRemainHidden(t *testing.T) {
	app := NewTestApp()
	app.view = "detail"
	app.detail = emptyDetailFixture()
	app.detailTab = detailTabRelated
	tt := renderTesterAndAdvance(t, app, 1600, 1000)
	if tt.HasText("同系列短剧") || tt.HasText("同一剧本改编") {
		t.Fatal("empty collections shown")
	}
	if !tt.HasText("原著小说") {
		t.Fatal("source novel missing")
	}
}

func TestDetailSkeletonAlignsToReadyHeaderAndCardShapes(t *testing.T) {
	loading := NewTestApp()
	loading.view = "detail"
	loading.detail = Detail{Loading: true}
	tt := ui.NewTester(loading.Render, 1600, 1000)
	tt.Advance(loadingLayoutSettle)
	skeleton, ok := tt.Find("详情内容")
	if !ok {
		t.Fatal("detail skeleton missing")
	}
	ready := NewTestApp()
	ready.view = "detail"
	ready.detail = detailWideFixture()
	rt := renderTesterAndAdvance(t, ready, 1600, 1000)
	hero, _ := rt.Find("宽屏详情")
	if absFloat(skeleton.Y-hero.Y) > 8 {
		t.Fatalf("loading content jumps: skeleton=%v hero=%v", skeleton, hero)
	}
	if tt.HasText("← 返回") || tt.HasText("正在加载详情") {
		t.Fatal("duplicate loading chrome")
	}
}

func absFloat(value float32) float32 {
	if value < 0 {
		return -value
	}
	return value
}

func TestDetailShowsAdaptationsAndOtherSeasons(t *testing.T) {
	app := NewTestApp()
	app.view = "detail"
	app.detail = detailWideFixture()
	app.detailTab = detailTabRelated
	app.detail.Adaptations.Items = nil
	for i := 1; i <= 14; i++ {
		app.detail.Adaptations.Items = append(app.detail.Adaptations.Items, CatalogItem{ID: fmt.Sprintf("version-%d", i), Title: fmt.Sprintf("其他改编版%d", i), EpisodeCount: 80})
	}
	tt := renderTesterAndAdvance(t, app, 1200, 1200)
	first, ok := tt.Find("其他改编版1")
	if !ok {
		t.Fatal("first adaptation missing")
	}
	last, ok := tt.Find("其他改编版14")
	if !ok || last.Y <= first.Y {
		t.Fatalf("later adaptations hidden horizontally: first=%v last=%v", first, last)
	}
	if !tt.HasText("共 14 部") || !tt.HasText("同系列其他季") || tt.HasText("当前剧集") {
		t.Fatal("missing season section or wrong adaptation count")
	}
	app.detailEpisodesList.ScrollToEnd()
	tt.Frame()
	if !tt.HasText("其他改编版14") {
		t.Fatal("final adaptation unreachable through page scroll")
	}
}

func TestDetailDurationAndResumeJump(t *testing.T) {
	app := NewTestApp()
	app.view = "detail"
	app.detail = convertDetail("123456", &ipc.SeriesDetailPageDTO{SeriesDetailDTO: ipc.SeriesDetailDTO{ID: "123456", Title: "时长详情", EpisodeCount: 812, Episodes: []ipc.EpisodeDTO{{ID: "episode-1", Number: 1, DurationSec: 129}}}})
	tt := scrollInputTester(app, 1200, 800)
	if !tt.HasText("02:09") || tt.HasText("00:00") {
		t.Fatal("duration lost or invented")
	}
	app.detail = longDetailEpisodesFixture(812)
	app.resume = CatalogItem{ID: app.detail.ID, ResumeEpisode: 731}
	tt.Frame()
	if err := tt.Click("定位第731集"); err != nil {
		t.Fatal(err)
	}
	waitFor(time.Second, func() bool { app.mu.Lock(); defer app.mu.Unlock(); return app.detailJumpEpisode == 731 })
	tt.Frame()
	if !tt.HasText("第731集") {
		t.Fatalf("resume jump failed: %v", tt.Texts())
	}
}
