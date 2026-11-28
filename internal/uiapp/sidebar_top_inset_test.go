package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The sidebar keeps room for the window controls above its content: the
// traffic lights on macOS, and nothing on Windows and Linux, whose window
// buttons sit in the top-right corner, away from the sidebar.
func TestSidebarTopInsetFollowsPlatformWindowControls(t *testing.T) {
	macBar := ui.TitleBar{Height: 28, Left: 78, Bottom: 28}
	windowsBar := ui.TitleBar{Height: 32, Right: 138, Bottom: 32}
	if got := sidebarTopInset(macBar); got != 28 {
		t.Fatalf("macOS sidebar keeps the traffic lights' band: got %v, want 28", got)
	}
	if got := sidebarTopInset(windowsBar); got != 0 {
		t.Fatalf("Windows sidebar keeps room for buttons that are not over it: got %v, want 0", got)
	}
	if got := sidebarTopInset(ui.TitleBar{}); got != 0 {
		t.Fatalf("a window without controls keeps no room: got %v, want 0", got)
	}
}

func TestWindowDragRegionFollowsPlatformWindowControls(t *testing.T) {
	if got := windowDragRegionHeight(ui.TitleBar{Height: 32, Right: 138}); got != 32 {
		t.Fatalf("Windows drag region height = %v, want 32", got)
	}
	if got := windowDragRegionHeight(ui.TitleBar{Height: 40, Right: 138}); got != 40 {
		t.Fatalf("tall title bar drag region height = %v, want 40", got)
	}
	if got := windowDragRegionHeight(ui.TitleBar{Height: 28, Left: 78}); got != 0 {
		t.Fatalf("macOS traffic-light title bar should use its existing drag region, got %v", got)
	}
	if got := windowDragRegionHeight(ui.TitleBar{}); got != 0 {
		t.Fatalf("window without title-bar controls should not add a drag region, got %v", got)
	}
}

func TestHostedScenesKeepGlobalDragRegionAboveContent(t *testing.T) {
	for _, scene := range []struct {
		name    string
		mediaOn bool
	}{
		{name: "recommend"},
		{name: "player", mediaOn: true},
	} {
		t.Run(scene.name, func(t *testing.T) {
			app := NewTestApp()
			app.SetMediaHost(&MediaHost{controller: newNativeUIPlayer()})
			app.view = "recommend"
			app.mediaOn = scene.mediaOn
			tt := ui.NewTester(app.Render, 1200, 800)
			tt.SetTitleBar(ui.TitleBar{Height: 32, Right: 138, Bottom: 32})

			dragRegion, ok := tt.Find("拖动窗口")
			if !ok || dragRegion.Y != 0 || dragRegion.H != 32 {
				t.Fatalf("global drag region missing or misplaced: %+v", dragRegion)
			}
			player, ok := tt.Find("原生播放器")
			if !ok || player.Y < 32 {
				t.Fatalf("hosted player overlaps the Windows title-bar band: %+v", player)
			}
		})
	}
}

func TestSidebarContentStartsBelowControlsOnWindows(t *testing.T) {
	app := NewTestApp()
	tt := ui.NewTester(app.Render, 1200, 800)
	// Windows draws its controls over the top-right corner, so the sidebar
	// starts below the full-width drag region while the controls remain clear.
	tt.SetTitleBar(ui.TitleBar{Height: 32, Right: 138, Bottom: 32})
	tt.Frame()
	discover, ok := tt.Find("发现")
	if !ok {
		t.Fatalf("sidebar section heading missing: %v", tt.Texts())
	}
	if discover.Y < 32 || discover.Y > 44 {
		t.Fatalf("sidebar content overlaps the Windows drag region: 发现 at Y=%v", discover.Y)
	}
}

func TestSidebarContentKeepsTrafficLightRoomOnMacOS(t *testing.T) {
	app := NewTestApp()
	tt := ui.NewTester(app.Render, 1200, 800)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 78, Bottom: 28})
	tt.Frame()
	discover, ok := tt.Find("发现")
	if !ok {
		t.Fatalf("sidebar section heading missing: %v", tt.Texts())
	}
	// 28 of traffic lights, then the heading's own 10 of padding.
	if discover.Y < 28 || discover.Y > 40 {
		t.Fatalf("macOS sidebar no longer clears the traffic lights: 发现 at Y=%v", discover.Y)
	}
}
