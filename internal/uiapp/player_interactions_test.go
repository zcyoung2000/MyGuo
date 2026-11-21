package uiapp

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/playback"
)

func TestNativeFeedPictureClickPausesThenResumesAndKeepsControls(t *testing.T) {
	p := newNativeUIPlayer()
	a := NewTestApp()
	a.SetMediaHost(&MediaHost{controller: p})
	a.OpenView("recommend")
	tt := ui.NewTester(a.Render, 1280, 820)
	tt.Advance(200 * time.Millisecond)
	for _, label := range []string{"清屏", "退出清屏", "详情"} {
		if _, ok := tt.AccessByRole(ui.RoleButton, label); ok {
			t.Fatalf("removed button remains: %s", label)
		}
	}
	stage, ok := tt.Find("播放画面")
	if !ok {
		t.Fatal("missing picture click target")
	}
	x, y := stage.X+stage.W/2, stage.Y+stage.H/2
	tt.ClickAt(x, y)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if !p.state.Paused || p.state.Preferences.Clear || !tt.HasText("播放进度") || !tt.HasText("推荐频道") {
		t.Fatalf("first tap: clear=%v paused=%v seek=%v channels=%v commands=%v stage=%+v", p.state.Preferences.Clear, p.state.Paused, tt.HasText("播放进度"), tt.HasText("推荐频道"), p.commands, stage)
	}
	if picture, ok := tt.AccessByRole(ui.RoleButton, "播放画面"); !ok || picture.Description != "点击继续播放" {
		t.Fatal("paused picture does not advertise the resume action")
	}
	tt.ClickAt(x, y)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if p.state.Paused || !p.state.Status.Playing || p.state.Preferences.Clear || !tt.HasText("播放进度") || !tt.HasText("推荐频道") {
		t.Fatal("second feed picture tap did not resume playback with controls visible")
	}
	if picture, ok := tt.AccessByRole(ui.RoleButton, "播放画面"); !ok || picture.Description != "点击暂停" {
		t.Fatal("playing picture does not advertise the pause action")
	}
	if len(p.commands) < 2 || p.commands[len(p.commands)-1] != "surface" {
		t.Fatalf("picture taps did not use the surface command: %v", p.commands)
	}
}

func TestNativeSeriesPictureOnlyTogglesControlsAndCenterButtonTogglesPlayback(t *testing.T) {
	p := newNativeUIPlayer()
	a := NewTestApp()
	a.SetMediaHost(&MediaHost{controller: p})
	a.OpenView("recommend")
	p.state.Mode = playback.Series
	p.state.Paused = false
	p.state.Status.Playing = true
	tt := ui.NewTester(a.Render, 1280, 820)
	tt.Advance(200 * time.Millisecond)
	if tt.HasText("已暂停") {
		t.Fatal("redundant paused status text is still visible")
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "暂停视频"); !ok {
		t.Fatal("playing series has no accessible center pause button")
	}
	stage, ok := tt.Find("播放画面")
	if !ok {
		t.Fatal("missing picture click target")
	}
	center, _ := tt.AccessByRole(ui.RoleButton, "暂停视频")
	if math.Abs(float64(center.Bounds.X+center.Bounds.W/2-(stage.X+stage.W/2))) > 2 || math.Abs(float64(center.Bounds.Y+center.Bounds.H/2-(stage.Y+stage.H/2))) > 2 {
		t.Fatalf("play/pause button is not centered over the video: button=%+v stage=%+v", center.Bounds, stage)
	}
	x, y := stage.X+stage.W*.24, stage.Y+stage.H*.5
	tt.ClickAt(x, y)
	tt.Frame()
	if !p.state.Preferences.Clear || p.state.Paused || !p.state.Status.Playing {
		t.Fatalf("picture click changed playback instead of hiding controls: %+v", p.state)
	}
	tt.Move(x+8, y+8)
	tt.Frame()
	if !p.state.Preferences.Clear || p.state.Paused || !p.state.Status.Playing {
		t.Fatalf("pointer movement revealed controls or interrupted playback: %+v", p.state)
	}
	tt.ClickAt(x, y)
	tt.Frame()
	if p.state.Preferences.Clear || p.state.Paused || !p.state.Status.Playing {
		t.Fatalf("picture click changed playback instead of showing controls: %+v", p.state)
	}
	if err := tt.Click("暂停视频"); err != nil {
		t.Fatal(err)
	}
	if !p.state.Paused || p.state.Status.Playing || p.commands[len(p.commands)-1] != "toggle" {
		t.Fatalf("center button did not pause video: state=%+v commands=%v", p.state, p.commands)
	}
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleButton, "播放视频"); !ok {
		t.Fatal("center button icon/name did not follow the paused state")
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "暂停视频"); ok {
		t.Fatal("paused video still exposes the pause action")
	}
}

func TestNativeTitleOpensCorrectDetailWithoutTogglingVideo(t *testing.T) {
	p := newNativeUIPlayer()
	a := NewTestApp()
	a.SetMediaHost(&MediaHost{controller: p})
	a.OpenView("recommend")
	tt := ui.NewTester(a.Render, 1280, 820)
	tt.Advance(200 * time.Millisecond)
	link, ok := tt.AccessByRole(ui.RoleLink, "查看《原生播放测试》详情")
	if !ok {
		t.Fatal("title is not an accessible detail link")
	}
	tt.ClickAt(link.Bounds.X+link.Bounds.W/2, link.Bounds.Y+link.Bounds.H/2)
	if a.View() != "detail" || a.DetailState().ID != "123456" {
		t.Fatalf("title opened wrong detail: %+v link=%+v commands=%v view=%s", a.DetailState(), link, p.commands, a.View())
	}
	for _, cmd := range p.commands {
		if cmd == "surface" || cmd == "toggle" {
			t.Fatal("title click fell through to playback")
		}
	}
}

func TestNativeOverlayControlsDoNotTriggerPictureClick(t *testing.T) {
	p := newNativeUIPlayer()
	a := NewTestApp()
	a.SetMediaHost(&MediaHost{controller: p})
	a.OpenView("recommend")
	tt := ui.NewTester(a.Render, 1280, 820)
	tt.Advance(200 * time.Millisecond)
	if err := tt.Click("选集"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if p.state.Panel != "episodes" || p.state.Preferences.Clear {
		t.Fatal("episode button fell through")
	}
	stage, _ := tt.Find("播放画面")
	tt.ClickAt(stage.X+stage.W/3, stage.Y+stage.H/2)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if p.state.Panel != "" || p.state.Paused || p.state.Preferences.Clear {
		t.Fatal("outside click failed to dismiss only the panel")
	}
}

func TestSpaceStillTogglesAfterClickingPicture(t *testing.T) {
	p := newNativeUIPlayer()
	a := NewTestApp()
	a.SetMediaHost(&MediaHost{controller: p})
	a.OpenView("recommend")
	tt := ui.NewTester(a.Render, 1280, 820)
	tt.Advance(200 * time.Millisecond)
	stage, _ := tt.Find("播放画面")
	tt.ClickAt(stage.X+stage.W/2, stage.Y+stage.H/2)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if p.state.Paused || p.state.Preferences.Clear || p.commands[len(p.commands)-1] != "toggle" {
		t.Fatalf("Space did not toggle playback after the picture click: %v", p.commands)
	}
}

func TestBlankOverlaySpacingStillClicksThePicture(t *testing.T) {
	for _, area := range []string{"channel-margin", "title-margin", "control-spacer"} {
		t.Run(area, func(t *testing.T) {
			p := newNativeUIPlayer()
			a := NewTestApp()
			a.SetMediaHost(&MediaHost{controller: p})
			a.OpenView("recommend")
			tt := ui.NewTester(a.Render, 1280, 820)
			tt.Advance(200 * time.Millisecond)
			stage, _ := tt.Find("播放画面")
			x, y := stage.X+20, float32(42)
			if area == "title-margin" {
				r, ok := tt.Find("查看《原生播放测试》详情")
				if !ok {
					t.Fatal("missing title link")
				}
				x, y = r.X+r.W+40, r.Y+r.H/2
			}
			if area == "control-spacer" {
				r, _ := tt.Find("00:48 / 03:20")
				x, y = r.X+r.W+30, r.Y+r.H/2
			}
			tt.ClickAt(x, y)
			tt.Frame()
			if !p.state.Paused || p.state.Preferences.Clear {
				t.Fatalf("blank overlay spacing did not pause playback: commands=%v point=%g,%g", p.commands, x, y)
			}
		})
	}
}
