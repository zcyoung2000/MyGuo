package uiapp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

func TestSourceNovelIsCompactAndFollowsDescription(t *testing.T) {
	for _, width := range []int{800, 1600} {
		for _, title := range []string{"短原著", strings.Repeat("长原著名称", 12)} {
			t.Run(fmt.Sprintf("%d-%d", width, len(title)), func(t *testing.T) {
				a := NewTestApp()
				a.view = "detail"
				a.detail = Detail{ID: "current", Title: "剧集名称", Description: "简介内容", Source: &ipc.SourceNovelDTO{Title: title, Tags: []string{"都市", "重生"}}}
				tt := scrollInputTester(a, width, 1000)
				tt.Frame()
				source, ok := tt.AccessByRole(ui.RoleGroup, "原著信息")
				if !ok {
					t.Fatal("missing source summary")
				}
				desc, _ := tt.Find("简介内容")
				play, _ := tt.Find("播放 / 继续观看")
				b := source.Bounds
				if b.Y < desc.Y+desc.H || b.Y+b.H > play.Y+1 {
					t.Fatalf("source not between description and actions: source=%v description=%v play=%v", b, desc, play)
				}
				if b.W > 420.5 || b.X+b.W > float32(width)-12 || b.W < 90 {
					t.Fatalf("source stretched or overflowed: %v", b)
				}
				if title == "短原著" && b.W > 300 {
					t.Fatalf("short source did not fit its content: %v", b)
				}
				for _, section := range detailRelatedSections(a.detail) {
					if section == "source" {
						t.Fatal("source repeated below related works")
					}
				}
				assertQuietLoading(t, tt)
				if width == 1600 && title == "短原著" {
					exportAuditScreenshot(t, tt, "detail-compact-source")
				}
			})
		}
	}
}
