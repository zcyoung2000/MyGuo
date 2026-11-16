package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

func TestDetailVerticalRhythmAndCollectionSpacing(t *testing.T) {
	app := NewTestApp()
	app.view = "detail"
	app.seriesID = "123456"
	app.detail = Detail{
		ID:          "123456",
		Title:       "紧凑详情页",
		Source:      &ipc.SourceNovelDTO{ID: "source", Title: "原著小说"},
		Actors:      []ipc.ActorInfo{{Name: "演员一"}},
		Seasons:     DetailCollection{Title: "同系列短剧", Items: []CatalogItem{{ID: "season-1", Title: "同系列短剧", Category: "漫剧"}}},
		Adaptations: DetailCollection{Title: "同一剧本改编", Items: []CatalogItem{{ID: "adapt-1", Title: "同一剧本改编剧", Category: "真人剧"}}},
		Episodes:    []CatalogItem{{ID: "123456", Title: "紧凑详情页", EpisodeID: "1"}},
	}
	app.sidebarWidth = 150
	tt := ui.NewTester(app.Render, 1000, 800)
	if tt.FramePending() {
		tt.Advance(200)
	}

	if err := tt.Click("相关作品"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	adaptations, ok := tt.Find("同系列作品")
	if !ok || adaptations.Y <= 0 || !tt.HasText("同系列其他季") {
		t.Fatalf("adaptation section missing or seasons mixed in: %v", tt.Texts())
	}
	if tt.HasText("漫剧") || tt.HasText("真人剧") {
		t.Fatalf("related cards should not show content type labels: %v", tt.Texts())
	}
	if !tt.HasText("同系列短剧") || !tt.HasText("同一剧本改编剧") {
		t.Fatalf("related card titles missing: %v", tt.Texts())
	}
	source, ok := tt.AccessByRole(ui.RoleGroup, "原著信息")
	if !ok || source.Bounds.Y <= adaptations.Y {
		t.Fatal("source novel not in related collection")
	}
}
