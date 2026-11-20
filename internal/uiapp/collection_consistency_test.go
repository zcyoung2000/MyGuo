package uiapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

func TestReservationTabCountsIncludeBothGroups(t *testing.T) {
	app := NewTestApp()
	app.view = "reservations"
	app.library.Reserved = Library{Loaded: true, Items: []CatalogItem{
		{ID: "released-1", Title: "已上线剧一"},
		{ID: "released-2", Title: "已上线剧二"},
		{ID: "pending-1", Title: "待上线剧", Upcoming: true},
	}}
	app.refreshVisibleLibraryLocked(libraryReservations)
	tt := ui.NewTesterWith(app.Render, 1200, 800, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	for _, label := range []string{"", "待上线 1", "已上线 2"} {
		if label != "" {
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
		}
		tt.Frame()
		tt.Advance(250 * time.Millisecond)
		if !tt.HasText("新剧预约 · 共 3 部 · 已上线 2 · 待上线 1") || !tt.HasText("已上线 2") || !tt.HasText("待上线 1") {
			t.Fatalf("tab counts changed with the active filter: %v", tt.Texts())
		}
		if pending := label == "待上线 1"; tt.HasText("待上线剧") != pending || tt.HasText("已上线剧一") == pending {
			t.Fatalf("tab counts or results disagree: %v", tt.Texts())
		}
		if tt.HasText("上线日期") {
			t.Fatal("reservations without a date show an empty date label")
		}
		if label == "待上线 1" {
			exportAuditScreenshot(t, tt, "reservation-counts-pending")
		} else {
			exportAuditScreenshot(t, tt, "reservation-counts-online")
		}
	}
}

func TestReservationCountsDistinguishLoadingFailureAndKnownEmpty(t *testing.T) {
	for _, test := range []struct {
		library Library
		want    string
	}{
		{Library{Loading: true}, "新剧预约 · 共 … 部 · 已上线 … · 待上线 …"},
		{Library{Error: "加载失败"}, "新剧预约 · 共 — 部 · 已上线 — · 待上线 —"},
		{Library{Loaded: true, Empty: true}, "新剧预约 · 共 0 部 · 已上线 0 · 待上线 0"},
		{Library{Loaded: true, Loading: true, Items: []CatalogItem{{ID: "one"}, {ID: "two", Upcoming: true}}}, "新剧预约 · 共 2 部 · 已上线 1 · 待上线 1"},
	} {
		app := NewTestApp()
		app.view = "reservations"
		app.library.Reserved = test.library
		tt := ui.NewTester(app.Render, 1200, 800)
		if !tt.HasText(test.want) {
			t.Fatalf("reservation counts misrepresent the load state: %v, want %s", tt.Texts(), test.want)
		}
	}
}

type collectionQueryProvider struct {
	featureProvider
	rank    func(context.Context, string, string, string) (ipc.RankResponse, error)
	release func(context.Context, string) (ipc.NewReleaseResponse, error)
}

func (p *collectionQueryProvider) GetRankings(ctx context.Context, content, board, cursor string) (ipc.RankResponse, error) {
	return p.rank(ctx, content, board, cursor)
}

func (p *collectionQueryProvider) GetNewReleases(ctx context.Context, date string) (ipc.NewReleaseResponse, error) {
	return p.release(ctx, date)
}

func TestRankingSwitchDoesNotShowPreviousBoardOnLoadingOrFailure(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "refresh", true: "different-board"}[changed], func(t *testing.T) {
			app := NewTestApp()
			app.view = "rank"
			app.rank = RankingState{Content: "all", Board: "old", Cursor: "old-page", HasMore: true, Items: []ipc.RankItemDTO{{SeriesID: "old", Title: "原榜单作品", Position: 1}}}
			tt := ui.NewTester(app.Render, 1200, 800)
			if changed {
				app.rankScroll.Y = 500
			}
			check := func() {
				t.Helper()
				tt.Frame()
				if tt.HasText("原榜单作品") == changed {
					t.Fatalf("changed=%t previous board is shown under the wrong selection: %v", changed, tt.Texts())
				}
				if changed && app.rankScroll.Y != 0 {
					t.Fatalf("new board retained the previous scroll position: %v", app.rankScroll.Y)
				}
			}
			app.featuresSource = &collectionQueryProvider{rank: func(context.Context, string, string, string) (ipc.RankResponse, error) {
				check()
				return ipc.RankResponse{}, errors.New("request failed")
			}}
			board := "old"
			if changed {
				board = "new"
			}
			app.SelectRank("all", board)
			check()
		})
	}
}

func TestCalendarSwitchDoesNotShowPreviousDateOnLoadingOrFailure(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "refresh", true: "different-date"}[changed], func(t *testing.T) {
			app := NewTestApp()
			app.view = "new"
			app.releases = ReleaseState{Calendar: true, Date: "20261120", Dates: []string{"20261120", "20261121"}, Items: []CatalogItem{{ID: "old", Title: "原日期新剧", Upcoming: true}}}
			tt := ui.NewTester(app.Render, 1200, 800)
			check := func() {
				t.Helper()
				tt.Frame()
				if tt.HasText("原日期新剧") == changed {
					t.Fatalf("changed=%t previous date is shown under the wrong selection: %v", changed, tt.Texts())
				}
			}
			app.featuresSource = &collectionQueryProvider{release: func(context.Context, string) (ipc.NewReleaseResponse, error) {
				check()
				return ipc.NewReleaseResponse{}, errors.New("request failed")
			}}
			date := "20261120"
			if changed {
				date = "20261121"
			}
			app.SelectRelease(true, date)
			check()
		})
	}
}

func TestCalendarUsesReturnedDateAndFallsBackToRequestedDate(t *testing.T) {
	for _, returned := range []string{"20261121", ""} {
		app := NewTestApp()
		app.featuresSource = &collectionQueryProvider{release: func(context.Context, string) (ipc.NewReleaseResponse, error) {
			return ipc.NewReleaseResponse{CurrentDate: returned, Items: []ipc.NewReleaseItemDTO{{SeriesID: "new", Title: "新剧"}}}, nil
		}}
		app.SelectRelease(true, "20261120")
		want := returned
		if want == "" {
			want = "20261120"
		}
		if app.releases.Date != want || len(app.releases.Items) != 1 || app.releases.Items[0].PublishAt != want {
			t.Fatalf("calendar selection and release metadata refer to different dates: %+v", app.releases)
		}
	}
}

func TestEmptyCalendarDescribesSelectedDay(t *testing.T) {
	app := NewTestApp()
	app.view = "new"
	app.releases = ReleaseState{Calendar: true, Date: "20261120", Dates: []string{"20261120"}}
	tt := ui.NewTester(app.Render, 1200, 800)
	if !tt.HasText("当日暂无新剧") || tt.HasText("当前新剧热榜暂无剧集") {
		t.Fatalf("calendar empty state describes the wrong view: %v", tt.Texts())
	}
	tt.Advance(250 * time.Millisecond)
	exportAuditScreenshot(t, tt, "calendar-empty-day")
}
