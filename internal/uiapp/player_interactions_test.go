package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestNativePictureClickCycleAndEscapeRestoreControls(t *testing.T) {
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
	if !p.state.Preferences.Clear || p.state.Paused || tt.HasText("播放进度") || tt.HasText("推荐频道") {
		t.Fatalf("first tap: clear=%v paused=%v seek=%v channels=%v commands=%v stage=%+v", p.state.Preferences.Clear, p.state.Paused, tt.HasText("播放进度"), tt.HasText("推荐频道"), p.commands, stage)
	}
	tt.ClickAt(x, y)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if !p.state.Paused || p.state.Preferences.Clear || !tt.HasText("播放进度") {
		t.Fatal("clean picture tap did not pause and restore controls")
	}
	tt.ClickAt(x, y)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if p.state.Paused || !p.state.Preferences.Clear {
		t.Fatal("paused picture did not resume clean playback")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if p.state.Preferences.Clear || !tt.HasText("播放进度") || p.commands[len(p.commands)-1] != "clear" {
		t.Fatal("Escape did not reveal controls before leaving playback")
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

func TestSpaceStillPausesAfterClickingPicture(t *testing.T) {
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
	if !p.state.Paused || p.state.Preferences.Clear || p.commands[len(p.commands)-1] != "toggle" {
		t.Fatalf("Space changed picture mode instead of toggling playback: %v", p.commands)
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
			if !p.state.Preferences.Clear || p.state.Paused {
				t.Fatalf("blank overlay spacing ate the click: commands=%v point=%g,%g", p.commands, x, y)
			}
		})
	}
}
