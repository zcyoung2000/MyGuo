package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
	"myguo/internal/playback"
)

func TestDetailEpisodeReflectsPartialPlaybackImmediately(t *testing.T) {
	app, tt := newDetailEpisodeTester(3)
	app.UpdateResumeProgress(ResumeProgress{SeriesID: app.detail.ID, EpisodeNum: 2, Position: .1, Sequence: 1})
	tt.Frame()
	for _, test := range []struct {
		name, description string
	}{{"第1集", ""}, {"第2集", "已看过"}, {"第3集", ""}} {
		info, ok := tt.AccessByRole(ui.RoleButton, test.name)
		if !ok || info.Description != test.description || info.Disabled {
			t.Fatalf("partial playback state for %s = %+v, found=%t", test.name, info, ok)
		}
	}
	// Zero-position updates and stale events must not create extra watched marks.
	app.UpdateResumeProgress(ResumeProgress{SeriesID: app.detail.ID, EpisodeNum: 1, Position: 0, Sequence: 2})
	app.UpdateResumeProgress(ResumeProgress{SeriesID: app.detail.ID, EpisodeNum: 3, Position: 1, Sequence: 1})
	if app.detail.Episodes[0].Watched || !app.detail.Episodes[1].Watched || app.detail.Episodes[2].Watched {
		t.Fatalf("invalid playback events changed episode watch state: %+v", app.detail.Episodes)
	}
	exportAuditScreenshot(t, tt, "detail-partial-watched")
}

func TestPlayerWatchedEpisodesStaySelectable(t *testing.T) {
	app := NewTestApp()
	state := playback.State{Mode: playback.Series, Book: ipc.SeriesCard{ID: "book"},
		Playback: &ipc.PlaybackDTO{EpisodeNum: 2}, Episodes: []ipc.EpisodeDTO{
			{Number: 1, Playable: true, Watched: true}, {Number: 2, Playable: true, Watched: true},
			{Number: 3, Playable: true}, {Number: 4, Playable: false},
		}}
	selected := 0
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(ui.Hex("#161616")).TextColor(ui.RGB(255, 255, 255)).Children(func() {
			app.renderNativeEpisodes(c, state, func(command string, value any) {
				if command == "episode" {
					selected = value.(int)
				}
			})
		})
	}, 360, 260)
	for _, test := range []struct {
		name, description string
		disabled          bool
	}{{"1", "已看过", false}, {"2", "已看过", false}, {"3", "", false}, {"4", "", true}} {
		info, ok := tt.AccessByRole(ui.RoleButton, test.name)
		if !ok || info.Description != test.description || info.Disabled != test.disabled {
			t.Fatalf("player episode %s state = %+v, found=%t", test.name, info, ok)
		}
	}
	if err := tt.Click("1"); err != nil || selected != 1 {
		t.Fatalf("watched episode cannot be replayed: selected=%d, err=%v", selected, err)
	}
	exportAuditScreenshot(t, tt, "player-partial-watched")
}

func TestDetailRefreshKeepsPlaybackWatchMarks(t *testing.T) {
	app, _ := newDetailEpisodeTester(3)
	app.detailSource = &fakeDetail{page: &ipc.SeriesDetailPageDTO{SeriesDetailDTO: ipc.SeriesDetailDTO{
		ID: app.detail.ID, Episodes: []ipc.EpisodeDTO{{Number: 1}, {Number: 2}, {Number: 3}},
	}}}
	app.UpdateResumeProgress(ResumeProgress{SeriesID: app.detail.ID, EpisodeNum: 1, Position: .5, Sequence: 1})
	app.UpdateResumeProgress(ResumeProgress{SeriesID: app.detail.ID, EpisodeNum: 2, Position: .5, Sequence: 2})
	app.loadDetail(app.detail.ID)
	if !app.detail.Episodes[0].Watched || !app.detail.Episodes[1].Watched || app.detail.Episodes[2].Watched {
		t.Fatalf("stale metadata erased live watch marks: %+v", app.detail.Episodes)
	}
}
