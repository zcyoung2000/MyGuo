package uiapp

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
	"myguo/internal/playback"
)

func feedEpisodeTester(t *testing.T) (*App, *nativeUIPlayer, *MediaHost, *ui.Tester) {
	t.Helper()
	p := newNativeUIPlayer()
	p.state.Book.EpisodeCount = 4
	p.state.Playback.EpisodeNum = 1
	for number := 1; number <= 4; number++ {
		p.state.Episodes = append(p.state.Episodes, ipc.EpisodeDTO{Number: number, Playable: true})
	}
	a := NewTestApp()
	host := &MediaHost{controller: p}
	a.SetMediaHost(host)
	a.feedChannel = "漫剧"
	a.OpenView("recommend")
	tt := ui.NewTester(a.Render, 1280, 820)
	tt.Advance(300 * time.Millisecond)
	return a, p, host, tt
}

func TestFeedEpisodeChoiceStartsSeriesSessionAndBackRestoresFeed(t *testing.T) {
	for _, number := range []int{1, 3} {
		t.Run(fmt.Sprintf("episode-%d", number), func(t *testing.T) {
			a, p, host, tt := feedEpisodeTester(t)
			if err := tt.Click("选集"); err != nil {
				t.Fatal(err)
			}
			tt.Advance(300 * time.Millisecond)
			if err := tt.Click(fmt.Sprint(number)); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			tt.Advance(300 * time.Millisecond)
			s := p.Snapshot()
			if !a.PlayerVisible() || a.View() != "recommend" || s.Mode != playback.Series || s.Session == 0 || s.Playback.EpisodeNum != number || s.Panel != "" || s.Paused {
				t.Fatalf("feed episode did not start an independent series session: visible=%v view=%s state=%+v", a.PlayerVisible(), a.View(), s)
			}
			if tt.HasText("推荐频道") {
				t.Fatal("recommendation channels remain over series playback")
			}
			if _, ok := tt.AccessByRole(ui.RoleButton, "暂停视频"); !ok {
				t.Fatal("series playback did not get its center play/pause button")
			}
			wantResume := any(nil)
			if number == 1 {
				wantResume = float64(48)
			}
			if host.desired["seriesId"] != "123456" || host.desired["resumeAt"] != wantResume {
				t.Fatalf("episode selection lost the series or resume intent: %+v", host.desired)
			}
			a.ClosePlayer(s.Session - 1)
			if !a.PlayerVisible() {
				t.Fatal("stale close dismissed the new series session")
			}
			a.ClosePlayer(s.Session)
			tt.Frame()
			tt.Advance(300 * time.Millisecond)
			if a.PlayerVisible() || p.Snapshot().Mode != playback.Feed || host.desired["event"] != "recommend" || host.desired["channel"] != "漫剧" || !tt.HasText("推荐频道") {
				t.Fatalf("back did not restore the original feed channel: visible=%v state=%+v scene=%+v", a.PlayerVisible(), p.Snapshot(), host.desired)
			}
		})
	}
}

func TestFeedSeriesSessionReturnsThroughHistoryAndSidebar(t *testing.T) {
	for _, route := range []string{"history-back", "recommend-sidebar"} {
		t.Run(route, func(t *testing.T) {
			a, p, host, _ := feedEpisodeTester(t)
			a.playFeedEpisode("123456", 2)
			if !a.PlayerVisible() {
				t.Fatal("series session did not start")
			}
			if route == "history-back" {
				a.NavigateHistory(-1)
			} else {
				a.OpenView("recommend")
			}
			if a.PlayerVisible() || p.Snapshot().Mode != playback.Feed || host.desired["event"] != "recommend" {
				t.Fatalf("%s left an idle or series player on the recommendation page", route)
			}
		})
	}
}

func TestFeedEpisodeChoiceRejectsStaleAndUnavailableEpisodes(t *testing.T) {
	a, p, _, _ := feedEpisodeTester(t)
	p.state.Episodes[2].Playable = false
	for _, choice := range []struct {
		series string
		number int
	}{{"stale-series", 1}, {"123456", 0}, {"123456", 5}, {"123456", 3}} {
		a.playFeedEpisode(choice.series, choice.number)
		if a.PlayerVisible() || p.Snapshot().Mode != playback.Feed {
			t.Fatalf("invalid choice started a series session: %+v", choice)
		}
	}
}

func TestAutomaticFeedContinuationHidesChannelsAndBackRestoresFeed(t *testing.T) {
	a, p, host, tt := feedEpisodeTester(t)
	p.state.Scene = 42
	a.ContinueFeedEpisode(41, "123456", 2)
	if a.PlayerVisible() {
		t.Fatal("stale automatic continuation opened a player")
	}
	a.ContinueFeedEpisode(42, "123456", 2)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	s := p.Snapshot()
	if !a.PlayerVisible() || s.Mode != playback.Series || s.Playback.EpisodeNum != 2 || tt.HasText("推荐频道") {
		t.Fatalf("automatic continuation retained discovery chrome: %+v", s)
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "暂停视频"); !ok {
		t.Fatal("series playback controls missing")
	}
	a.ClosePlayer(s.Session)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if a.PlayerVisible() || p.Snapshot().Mode != playback.Feed || host.desired["channel"] != "漫剧" || !tt.HasText("推荐频道") {
		t.Fatal("back did not return to original feed channel")
	}
}
