package uiapp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

func TestSourceNovelLeadsRelatedWorksAndDateMovesToStats(t *testing.T) {
	for _, width := range []int{800, 1600} {
		for _, title := range []string{"短原著", strings.Repeat("长原著名称", 12)} {
			t.Run(fmt.Sprintf("%d-%d", width, len(title)), func(t *testing.T) {
				a := NewTestApp()
				a.view = "detail"
				a.detail = Detail{ID: "current", Title: "剧集名称", Description: "简介内容", Heat: "100万热度", PublishText: "2026-11-08", Source: &ipc.SourceNovelDTO{ID: "source", Title: title, Tags: []string{"都市", "重生"}}, Adaptations: DetailCollection{Title: "同一剧本改编", Items: []CatalogItem{{ID: "version", Title: "其他版本"}}}}
				tt := scrollInputTester(a, width, 1000)
				tt.Frame()
				if tt.HasText(title) || tt.HasText("原著小说") {
					t.Fatal("source novel remains in introduction")
				}
				date, ok := tt.Find("2026-11-08")
				if !ok {
					t.Fatal("date missing")
				}
				heat, _ := tt.Find("100万热度")
				desc, _ := tt.Find("简介内容")
				if absFloat(date.Y+date.H/2-heat.Y-heat.H/2) > 1 || date.Y >= desc.Y {
					t.Fatalf("date not in statistics row: date=%v heat=%v description=%v", date, heat, desc)
				}
				if err := tt.Click("相关作品"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(loadingLayoutSettle)
				source, ok := tt.AccessByRole(ui.RoleGroup, "原著信息")
				if !ok {
					t.Fatal("missing source summary")
				}
				b := source.Bounds
				version, ok := tt.AccessByRole(ui.RoleButton, "其他版本")
				if !ok {
					t.Fatal("adaptation missing")
				}
				if absFloat(b.Y-version.Bounds.Y) > 1 || b.X >= version.Bounds.X || absFloat(b.W-version.Bounds.W) > 1 {
					t.Fatalf("novel is not first same-sized card: novel=%v version=%v", b, version.Bounds)
				}
				poster, ok := tt.Find("poster:source-novel:source|")
				if !ok {
					t.Fatal("novel poster missing")
				}
				badge, ok := tt.Find("原著小说")
				if !ok || badge.Y < poster.Y || badge.Y+badge.H > poster.Y+poster.H {
					t.Fatalf("novel label not over poster: badge=%v poster=%v", badge, poster)
				}
				if !tt.HasText("同系列作品") || tt.HasText("同一剧本改编") || !tt.HasText(title) {
					t.Fatal("related labels or source title incorrect")
				}
				tt.ClickAt(b.X+b.W/2, b.Y+10)
				tt.Frame()
				if a.DetailState().ID != "current" {
					t.Fatal("novel navigated to a drama detail")
				}
				assertQuietLoading(t, tt)
				if title == "短原著" {
					exportAuditScreenshot(t, tt, fmt.Sprintf("detail-source-first-%d", width))
				}
			})
		}
	}
}
