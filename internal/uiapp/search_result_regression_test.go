package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

func TestSearchResultsMergeSeriesAndExcludeKnownNonVideoCards(t *testing.T) {
	items := []CatalogItem{
		{ID: "A", Title: "首个剧集", EpisodeID: "episode-one"},
		{ID: "book", Title: "原著小说", Kind: "source_novel", EpisodeCount: 999},
		{ID: "actor", Title: "演员", ActorID: "actor"},
		{ID: "post", Title: "讨论推广", Kind: "post"},
		{ID: "B", Title: "同名剧集", EpisodeCount: 40},
		{ID: "A", Title: "首个剧集", EpisodeID: "episode-two", CoverURL: "poster", EpisodeCount: 80},
		{ID: "C", Title: "同名剧集", EpisodeCount: 60},
	}
	got := normalizeSearchCards(items)
	if len(got) != 3 || got[0].ID != "A" || got[1].ID != "B" || got[2].ID != "C" || got[0].CoverURL != "poster" || got[0].EpisodeCount != 80 {
		t.Fatalf("search did not merge stable series identities: %+v", got)
	}
	if items[0].CoverURL != "" {
		t.Fatal("normalization mutated source results")
	}
}

func TestSearchFirstCardRemainsClickableAfterDetailBack(t *testing.T) {
	a := NewTestApp()
	a.SetQuery("物价贬值")
	a.SearchSubmitted()
	// Mixed API cells may repeat an exact match in the top hit and result list.
	a.search = CatalogPage{Items: []CatalogItem{
		{ID: "A", Title: "物价贬值100万倍，我余额不变", EpisodeCount: 80},
		{ID: "B", Title: "物价贬值100万倍，人生开挂", EpisodeCount: 40},
		{ID: "A", Title: "物价贬值100万倍，我余额不变", EpisodeCount: 80},
		{ID: "novel", Title: "搜索推广小说", Kind: "source_novel", EpisodeCount: 999},
		{ID: "post", Title: "搜索推广讨论", Kind: "post"},
	}}
	tt := ui.NewTesterWith(a.Render, 1200, 800, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	for round := range 2 {
		if tt.HasText("搜索推广小说") || tt.HasText("搜索推广讨论") {
			t.Fatal("search rendered a non-video result")
		}
		count := 0
		for _, node := range tt.AccessAll() {
			if node.Name == "物价贬值100万倍，我余额不变" && node.Role == ui.RoleButton {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("round %d: duplicate search card identities: %d", round, count)
		}
		if err := tt.Click("物价贬值100万倍，我余额不变"); err != nil {
			t.Fatal(err)
		}
		waitFor(time.Second, func() bool { return a.View() == "detail" })
		if a.View() != "detail" {
			t.Fatal("first result could not open detail")
		}
		a.NavigateHistory(-1)
		tt.Frame()
		tt.Frame()
		if a.View() != "search" {
			t.Fatal("back lost search navigation")
		}
	}
}

func TestNewReleasesDeduplicateSeriesCards(t *testing.T) {
	provider := &parityProvider{releaseResult: ipc.NewReleaseResponse{
		CurrentDate: "20261125",
		Items: []ipc.NewReleaseItemDTO{
			{SeriesID: "same", Title: "重复新剧一", Online: true},
			{SeriesID: "same", Title: "重复新剧二", Online: true},
			{SeriesID: "other", Title: "另一部新剧", Online: true},
		},
	}}
	a := NewTestApp()
	a.featuresSource = provider
	a.releases = ReleaseState{Calendar: true, Date: "20261125"}
	a.LoadReleases()

	if len(a.releases.Items) != 2 || a.releases.Items[0].ID != "same" || a.releases.Items[1].ID != "other" {
		t.Fatalf("new releases kept duplicate series cards: %+v", a.releases.Items)
	}
}
