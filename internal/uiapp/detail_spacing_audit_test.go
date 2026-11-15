package uiapp

import (
	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
	"testing"
)

var SourceNovelForSpacingAudit = ipc.SourceNovelDTO{ID: "source-novel", Title: "重生：我远离后，她怎么慌了？"}

func TestDetailCollectionVerticalSpacingAudit(t *testing.T) {
	app := NewTestApp()
	app.view = "detail"
	app.detailTab = detailTabRelated
	app.detail = Detail{ID: "123456", Title: "改编版本间距审计", Source: &SourceNovelForSpacingAudit,
		Adaptations: DetailCollection{Title: "同一剧本改编", Items: []CatalogItem{
			{ID: "adaptation-1", Title: "另一改编版本", EpisodeCount: 80, Heat: "300万热度"},
			{ID: "adaptation-2", Title: "另一待上线版本", Upcoming: true, Reserved: true},
		}},
	}
	tt := ui.NewTester(app.Render, 1000, 1100)
	tt.Advance(loadingLayoutSettle)
	tt.Frame()
	heading, ok := tt.Find("同一剧本改编")
	poster, pOK := tt.Find("poster:adaptation-1|")
	meta, mOK := tt.Find("80集 · 300万热度")
	if !ok || !pOK || !mOK {
		t.Fatalf("adaptation content missing: %v", tt.Texts())
	}
	gap := poster.Y - heading.Y - heading.H
	if gap < 6 || gap > 12 || absFloat(poster.H/poster.W-4.0/3.0) > .04 {
		t.Fatalf("poster spacing/ratio wrong: heading=%v poster=%v", heading, poster)
	}
	if meta.Y < poster.Y+poster.H || !tt.HasText("取消预约") {
		t.Fatal("metadata or upcoming action missing")
	}
	exportAuditScreenshot(t, tt, "detail-adaptations")
}
