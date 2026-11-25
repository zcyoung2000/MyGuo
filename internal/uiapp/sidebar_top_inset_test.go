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

func TestSidebarContentStartsBelowControlsOnWindows(t *testing.T) {
	app := NewTestApp()
	tt := ui.NewTester(app.Render, 1200, 800)
	// Windows draws minimize, maximize and close over the top-right corner:
	// no room is kept at the top-left, where the sidebar is.
	tt.SetTitleBar(ui.TitleBar{Height: 32, Right: 138, Bottom: 32})
	tt.Frame()
	discover, ok := tt.Find("发现")
	if !ok {
		t.Fatalf("sidebar section heading missing: %v", tt.Texts())
	}
	if discover.Y < 0 || discover.Y > 8 {
		t.Fatalf("sidebar content keeps the traffic lights' empty band: 发现 at Y=%v", discover.Y)
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
