package uiapp

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

func TestSettingsMutationAfterRealNavigation(t *testing.T) {
	app := NewTestApp()
	fake := &settingsTestProvider{profile: ipc.UserProfile{UserID: "42", Nickname: "New User", LoggedIn: true}}
	app.settingsTestProvider = fake
	app.OpenView("settings")
	app.LoadSettings()
	app.runSettingsMutation("test-action", app.generation, func(context.Context) (string, error) { fake.profile.Nickname = "Updated User"; return "success", nil })
	state := app.SettingsState()
	if state.Loading || state.Profile.Nickname != "Updated User" {
		t.Fatalf("navigation generation=%d left settings stale: loading=%t nickname=%q success=%q", app.generation, state.Loading, state.Profile.Nickname, state.Success)
	}
}
func TestSettingsProfileFitsNarrowWindow(t *testing.T) {
	for _, entry := range []struct {
		name           string
		width, sidebar int
		nickname       string
	}{
		{"settings-minimum", 1000, 220, "Henry"},
		{"settings-wide-sidebar", 1000, 420, "名字很长的用户昵称名字很长的用户昵称"},
	} {
		app := NewTestApp()
		app.view = "settings"
		app.sidebarWidth = float32(entry.sidebar)
		app.settings = Settings{Profile: ipc.UserProfile{UserID: "710000000000001", Nickname: entry.nickname, LoggedIn: true}, LoginOpen: true}
		tt := ui.NewTester(app.Render, entry.width, 640)
		tt.SetDark(true)
		tt.Frame()
		tt.Frame()
		badge, badgeOK := tt.Find("已登录官方账号")
		refresh, refreshOK := tt.Find("刷新资料")
		if !badgeOK || !refreshOK {
			t.Fatalf("%s: profile controls missing: %v", entry.name, tt.Texts())
		}
		if badge.H > 23 || badge.X+badge.W > float32(entry.width) {
			t.Fatalf("%s: status label wraps/overflows: badge=%+v", entry.name, badge)
		}
		if entry.sidebar == 420 && refresh.Y < badge.Y+badge.H {
			t.Fatalf("narrow profile actions did not stack: action=%+v status=%+v", refresh, badge)
		}
		exportAuditScreenshot(t, tt, entry.name)
	}
}

func TestShortDetailShouldNotHaveEmptyEpisodeScroll(t *testing.T) {
	app := NewTestApp()
	app.view = "detail"
	app.detail = detailWideFixture()
	app.detail.Episodes = app.detail.Episodes[:2]
	tt := ui.NewTester(app.Render, 1600, 1000)
	tt.SetDark(true)
	tt.Frame()
	tt.Frame()
	exportAuditScreenshot(t, tt, "detail-two-episodes")
	if app.detailScroll.MaxY > 0 {
		t.Fatalf("only two episodes create %.0f pixels of empty scroll", app.detailScroll.MaxY)
	}
}

func exportAuditScreenshot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("MYGUO_FIX_SCREENSHOTS")
	if dir == "" {
		return
	}
	file, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsMutationDoesNotUpdateAfterNavigation(t *testing.T) {
	app := NewTestApp()
	app.settingsTestProvider = &settingsTestProvider{}
	app.OpenView("settings")
	app.LoadSettings()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	version := app.generation
	go func() {
		defer close(done)
		app.runSettingsMutation("test-action", version, func(context.Context) (string, error) {
			close(started)
			<-release
			return "stale success", nil
		})
	}()
	<-started
	app.OpenView("discover")
	close(release)
	<-done
	if state := app.SettingsState(); state.Success != "" || state.Loading {
		t.Fatalf("stale operation changed settings after leaving: %+v", state)
	}
}

func TestDetailStatsDoNotRenderUnitsWithoutCounts(t *testing.T) {
	app := NewTestApp()
	app.view = "detail"
	app.detail = Detail{ID: "123456", Title: "No counts"}
	tt := ui.NewTester(app.Render, 1200, 800)
	tt.Frame()
	for _, text := range tt.Texts() {
		if text == "人在追 · 赞" || text == "人在追" || text == "赞" {
			t.Fatalf("empty count rendered: %q", text)
		}
	}
}

func TestDetailRefreshPreservesLiveResume(t *testing.T) {
	app := NewTestApp()
	app.detailSource = &fakeDetail{page: &ipc.SeriesDetailPageDTO{SeriesDetailDTO: ipc.SeriesDetailDTO{ID: "123456"}, ResumeEpisode: 2, ResumePosition: 5}}
	app.OpenDetail("123456")
	app.UpdateResumeProgress(ResumeProgress{SeriesID: "123456", EpisodeNum: 8, Position: 72.5, Sequence: 1})
	app.loadDetail("123456")
	if app.resume.ResumeEpisode != 8 || app.resume.ResumePosition != 72.5 {
		t.Fatalf("detail refresh overwrote live player progress: %+v", app.resume)
	}
}

func TestDetailResumeAfterRealNavigation(t *testing.T) {
	app := NewTestApp()
	host := &MediaHost{}
	app.SetMediaHost(host)
	app.detailSource = &fakeDetail{page: &ipc.SeriesDetailPageDTO{SeriesDetailDTO: ipc.SeriesDetailDTO{ID: "123456", Title: "Resume", EpisodeCount: 10}, ResumeEpisode: 8, ResumePosition: 72.5}}
	app.OpenDetail("123456")
	app.loadDetail("123456")
	app.PlayDetail()
	if host.desired["episodeNum"] != 8 || host.desired["resumeAt"] != 72.5 {
		t.Fatalf("actual navigation ignored saved progress: episode=%v position=%v resume=%+v", host.desired["episodeNum"], host.desired["resumeAt"], app.resume)
	}
}

func TestSettingsMutationRejectsNavigationBeforeWorkerStarts(t *testing.T) {
	app := NewTestApp()
	app.settingsTestProvider = &settingsTestProvider{}
	app.OpenView("settings")
	app.LoadSettings()
	version := app.generation
	app.OpenView("discover")
	app.runSettingsMutation("test-action", version, func(context.Context) (string, error) { return "old page", nil })
	if state := app.SettingsState(); state.Success != "" || state.Loading {
		t.Fatalf("late worker changed the next page: %+v", state)
	}
}
