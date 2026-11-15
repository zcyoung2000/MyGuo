package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestPlayerChoicesUseSystemMenusAndDoNotOpenDrawer(t *testing.T) {
	p := newNativeUIPlayer()
	a := NewTestApp()
	a.SetMediaHost(&MediaHost{controller: p})
	a.OpenView("recommend")
	tt := ui.NewTester(a.Render, 1000, 640)
	tt.Advance(300 * time.Millisecond)
	if err := tt.Click("1080p"); err != nil {
		t.Fatal(err)
	}
	if len(tt.Menu()) != 2 {
		t.Fatalf("quality did not open a small system menu: %v", tt.Menu())
	}
	if err := tt.ChooseMenuItem("720p"); err != nil {
		t.Fatal(err)
	}
	if p.values["quality"] != "720p" || p.state.Panel != "" {
		t.Fatalf("choice did not apply directly: %+v", p.values)
	}
	if _, ok := tt.Find("720p"); ok {
		t.Fatal("menu did not close after choosing")
	}
	if err := tt.Click("1×"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("1.5×"); err != nil {
		t.Fatal(err)
	}
	if p.values["speed"] != 1.5 {
		t.Fatalf("rate was not applied: %+v", p.values)
	}
	if err := tt.Click("1×"); err != nil {
		t.Fatal(err)
	}
	tt.CloseMenu()
	if len(tt.Menu()) != 0 {
		t.Fatal("dismissing the system menu left it open")
	}
	if _, ok := p.values["close"]; ok {
		t.Fatal("closing a popup also closed playback")
	}
	if _, ok := p.values["toggle"]; ok {
		t.Fatal("dismissing menu also paused playback")
	}
}

func TestUnavailableQualityCannotBeChosen(t *testing.T) {
	p := newNativeUIPlayer()
	p.state.Playback.Qualities[0].Available = false
	a := NewTestApp()
	a.SetMediaHost(&MediaHost{controller: p})
	a.OpenView("recommend")
	tt := ui.NewTester(a.Render, 1280, 820)
	tt.Click("1080p")
	if err := tt.ChooseMenuItem("720p"); err == nil {
		t.Fatal("unavailable quality was selectable")
	}
	if _, ok := p.values["quality"]; ok {
		t.Fatal("disabled rendition changed playback")
	}
}

func TestContentPanelRemainsReadableAboveBrightBackground(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.RGB(255, 255, 255)).Children(func() {
			contentSurface(ui.Box(c).Absolute().Left(20).Top(20).Size(140, 100).Radius(18))
		})
	}, 200, 150)
	color := tt.Image().RGBAAt(80, 70)
	if color.R > 30 || color.G > 30 || color.B > 30 || color.A != 255 {
		t.Fatalf("bright backdrop obscures panel text: %+v", color)
	}
	tt.SetPreferences(ui.Preferences{ReduceTransparency: true})
	tt.Frame()
	color = tt.Image().RGBAAt(80, 70)
	if color.R != 0 || color.G != 0 || color.B != 0 {
		t.Fatalf("Reduce Transparency must give the panel a solid black base: %+v", color)
	}
}

func TestSharedTabsRetainKeyboardSelection(t *testing.T) {
	a := NewTestApp()
	selected := "all"
	tt := ui.NewTester(func(c *ui.Context) {
		a.renderSlidingTabs(c, "test-tabs", "test", []slidingTabItem{{"all", "全部"}, {"pending", "未看完"}, {"done", "已看完"}}, selected, func(key string) { selected = key })
	}, 500, 200)
	if err := tt.Click("未看完"); err != nil {
		t.Fatal(err)
	}
	if selected != "pending" {
		t.Fatal("click did not select tab")
	}
	tt.Key(0, ui.KeyRight)
	if selected != "done" {
		t.Fatal("arrow key did not select the next tab")
	}
}
