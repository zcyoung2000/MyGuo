package uiapp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

func TestCalendarInitialSkeletonUsesGridIndependentOfDates(t *testing.T) {
	for _, dates := range [][]string{nil, {"20261124"}, {"20261124", "20261125", "20261126"}} {
		app := NewTestApp()
		app.view = "new"
		app.releases = ReleaseState{Calendar: true, Loading: true, Dates: dates}
		tt := ui.NewTesterWith(app.Render, 1200, 800, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
		bone, ok := tt.AccessByRole(ui.RoleImage, "加载海报占位")
		if !ok || bone.Bounds.W >= newReleaseSparsePosterWidth {
			t.Fatalf("unknown result used a feature poster: dates=%v poster=%+v", dates, bone)
		}
		before := bone.Bounds
		exportAuditScreenshot(t, tt, fmt.Sprintf("calendar-grid-loading-%d-dates", len(dates)))
		app.releases.Loading = false
		for i := range 12 {
			app.releases.Items = append(app.releases.Items, CatalogItem{ID: fmt.Sprint(i), Title: fmt.Sprintf("新剧%d", i)})
		}
		tt.Frame()
		tt.Frame()
		ready, ok := tt.Find("poster:0|")
		if !ok || absFloat(before.X-ready.X) > 2 || absFloat(before.Y-ready.Y) > 2 ||
			absFloat(before.W-ready.W) > 2 || absFloat(before.H-ready.H) > 2 {
			t.Fatalf("grid placeholder differs from result: bone=%+v ready=%+v", before, ready)
		}
		exportAuditScreenshot(t, tt, fmt.Sprintf("calendar-grid-ready-%d-dates", len(dates)))
	}
}

func TestCalendarDateLoadKeepsSparseShapeAndMetadata(t *testing.T) {
	for _, scenario := range []struct {
		height  int
		wrapped bool
	}{{600, false}, {800, false}, {800, true}} {
		name := fmt.Sprintf("%d-wrapped-%t", scenario.height, scenario.wrapped)
		t.Run(name, func(t *testing.T) {
			height := scenario.height
			app := NewTestApp()
			app.view = "new"
			item := CatalogItem{ID: "previous", Title: "昨日新剧", PublishAt: "20261124", Category: "都市", EpisodeCount: 80, Description: "新剧简介"}
			if scenario.wrapped {
				item.Title = strings.Repeat("新剧标题", 8)
				item.Description = strings.Repeat("这是新剧的多行简介。", 12)
			}
			app.releases = ReleaseState{Calendar: true, Date: "20261124", Dates: []string{"20261124", "20261125", "20261126"}, Items: []CatalogItem{item}}
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			finish := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(finish)
			app.featuresSource = &collectionQueryProvider{release: func(context.Context, string) (ipc.NewReleaseResponse, error) {
				close(started)
				<-release
				return ipc.NewReleaseResponse{CurrentDate: "20261125", Dates: app.releases.Dates, Items: []ipc.NewReleaseItemDTO{
					{SeriesID: "next", Title: item.Title, Category: item.Category, EpisodeCount: item.EpisodeCount, Note: item.Description},
				}}, nil
			}}
			go func() { defer close(done); app.SelectRelease(true, "20261125") }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("date request did not start")
			}
			tt := ui.NewTesterWith(app.Render, 1200, height, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
			var posters []ui.AccessInfo
			for _, node := range tt.AccessAll() {
				if node.Name == "加载海报占位" {
					posters = append(posters, node)
				}
			}
			if len(posters) != 1 {
				t.Fatalf("date navigation count determined skeleton count: %d", len(posters))
			}
			action, ok := tt.Find("加载预约占位")
			if !ok {
				t.Fatal("reservation placeholder missing")
			}
			if _, ok := tt.Find("加载新剧episodes"); !ok {
				t.Fatal("episode metadata placeholder missing")
			}
			if _, ok := tt.Find("加载新剧description"); ok != (height >= 700) {
				t.Fatal("description does not follow ready layout")
			}
			exportAuditScreenshot(t, tt, "calendar-sparse-loading-"+name)
			finish()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("date request did not finish")
			}
			tt.Frame()
			tt.Frame()
			poster, ok := tt.AccessByRole(ui.RoleImage, "poster:next|")
			if !ok || absFloat(posters[0].Bounds.Y-poster.Bounds.Y) > 2 || absFloat(posters[0].Bounds.H-poster.Bounds.H) > 2 {
				t.Fatalf("poster moved: loading=%+v ready=%+v", posters[0].Bounds, poster.Bounds)
			}
			var readyAction ui.Rect
			ok = false
			for _, node := range tt.AccessAll() {
				if node.Role == ui.RoleButton && node.Name == "预约" && node.Bounds.X > defaultSidebarWidth {
					readyAction, ok = node.Bounds, true
					break
				}
			}
			if !ok || absFloat(action.Y-readyAction.Y) > 2 || absFloat(action.H-readyAction.H) > 2 || absFloat(action.W-readyAction.W) > 2 {
				t.Fatalf("reservation moved: loading=%+v ready=%+v", action, readyAction)
			}
			exportAuditScreenshot(t, tt, "calendar-sparse-ready-"+name)
		})
	}
}
