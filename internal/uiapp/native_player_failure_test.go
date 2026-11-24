package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/playback"
)

func TestPlaybackErrorsShowCompactStatusAndKeepPlaybackUsable(t *testing.T) {
	for _, mode := range []playback.Mode{playback.Feed, playback.Series} {
		p := newNativeUIPlayer()
		p.state.Mode = mode
		p.state.Error = "视频暂时无法加载，请稍后重试"
		a := NewTestApp()
		a.SetMediaHost(&MediaHost{controller: p})
		a.OpenView("recommend")
		p.state.Mode = mode
		tt := ui.NewTester(a.Render, 1280, 820)
		tt.Advance(200 * time.Millisecond)
		if !tt.HasText(p.state.Error) {
			t.Fatal("exhausted playback failure remains invisible")
		}
		stage, ok := tt.AccessByRole(ui.RoleButton, "播放画面")
		if !ok {
			t.Fatal("picture remains inaccessible")
		}
		if stage.Description == "请使用重新加载按钮重试" {
			t.Fatal("assistive text points to removed prompt")
		}
		status, ok := tt.AccessByRole(ui.RoleStatus, "播放状态")
		if !ok || status.Bounds.H >= stage.Bounds.H/3 || status.Bounds.W >= stage.Bounds.W {
			t.Fatal("playback status is absent or covers the picture")
		}
		if err := tt.Click("重试"); err != nil {
			t.Fatal(err)
		}
		if len(p.commands) == 0 || p.commands[len(p.commands)-1] != "play" {
			t.Fatal("status retry did not request playback")
		}
		if err := tt.Click("播放"); err != nil {
			t.Fatal(err)
		}
		if len(p.commands) == 0 || p.commands[len(p.commands)-1] != "toggle" {
			t.Fatal("ordinary play control could not request recovery")
		}
		if err := tt.Click("下一集"); err != nil {
			t.Fatal(err)
		}
		if len(p.commands) == 0 || p.commands[len(p.commands)-1] != "nextEpisode" {
			t.Fatal("failure blocked navigation")
		}
		p.state.Error = ""
		tt.Frame()
		if tt.HasText("重试") || tt.HasText("视频暂时无法加载，请稍后重试") {
			t.Fatal("recovered playback retained the failure status")
		}
	}
}

func TestPlaybackLoadingAndPauseHaveVisibleStatus(t *testing.T) {
	p := newNativeUIPlayer()
	p.state.Loading = true
	p.state.Status.Ready, p.state.Status.Displayed = false, false
	a := NewTestApp()
	a.SetMediaHost(&MediaHost{controller: p})
	a.OpenView("recommend")
	tt := ui.NewTester(a.Render, 1280, 820)
	if !tt.HasText("正在加载视频") {
		t.Fatal("slow source has no loading feedback")
	}
	if _, ok := tt.AccessByRole(ui.RoleProgress, "正在加载视频"); !ok {
		t.Fatal("loading has no accessible progress indicator")
	}
	p.state.Paused = true
	tt.Frame()
	if !tt.HasText("已暂停") || tt.HasText("正在加载视频") {
		t.Fatal("intentional pause is shown as a pending load")
	}
}
