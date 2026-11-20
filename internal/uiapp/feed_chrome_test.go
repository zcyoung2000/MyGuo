package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/playback"
)

func TestNativeFeedChannelSelectionSurvivesNavigationAndStartup(t *testing.T) {
	app := NewTestApp()
	host := &MediaHost{}
	app.SetMediaHost(host)
	app.OpenView("recommend")
	tt := ui.NewTester(app.Render, 1200, 800)
	if err := tt.Click("漫剧"); err != nil {
		t.Fatal(err)
	}
	waitFor(time.Second, func() bool { app.mu.Lock(); defer app.mu.Unlock(); return app.feedChannel == "漫剧" })
	app.mu.Lock()
	selected := app.feedChannel
	app.mu.Unlock()
	if selected != "漫剧" || host.desired["channel"] != "漫剧" {
		t.Fatalf("native selection didn't reach the existing feed: %q %+v", selected, host.desired)
	}
	app.OpenView("discover")
	app.SelectFeedChannel("真人剧")
	app.OpenView("recommend")
	if host.desired["channel"] != "漫剧" {
		t.Fatal("returning to recommend lost the selected channel")
	}
	app.SelectFeedChannel("invalid")
	if host.desired["channel"] != "漫剧" {
		t.Fatal("invalid channel changed the recommendation source")
	}
	if host.Snapshot().Mode != playback.Feed {
		t.Fatal("native feed not selected immediately")
	}

}

func TestNativeFeedChannelsOverlayKeepsMediaAndHidesWithItsScene(t *testing.T) {
	app := NewTestApp()
	player := newNativeUIPlayer()
	app.SetMediaHost(&MediaHost{controller: player})
	app.OpenView("recommend")
	tt := ui.NewTester(app.Render, 1200, 800)
	channels, ok := tt.Find("推荐频道")
	if !ok || tt.WebViewAt(channels.X+channels.W/2, channels.Y+channels.H/2) != nil {
		t.Fatal("native channel control doesn't take pointer input over the WebView")
	}
	if !tt.HasText("原生播放器") || tt.WebViewAt(900, 300) != nil {
		t.Fatal("native channel overlay changes the full media viewport")
	}
	player.state.Panel = "comments"
	tt.Frame()
	moved, _ := tt.Find("推荐频道")
	if channels.X != moved.X || channels.Y != moved.Y {
		t.Fatal("overlay drawer must not shift the channel control or video stage")
	}
	player.state.Preferences.Clear = true
	tt.Frame()
	if !tt.HasText("推荐频道") || !tt.HasText("播放进度") {
		t.Fatal("recommendation controls are not persistent")
	}
	player.state.Preferences.Clear = false
	app.detail = Detail{ID: "123456"}
	app.PlayDetail()
	tt.Frame()
	if tt.HasText("推荐频道") {
		t.Fatal("recommendation channels cover the full player")
	}
	app.OpenView("settings")
	tt.Frame()
	if tt.HasText("推荐频道") {
		t.Fatal("recommendation channels cover settings")
	}
}

func TestAmbientGlowUpdatesFeedChrome(t *testing.T) {
	app := NewTestApp()
	shell := NativeShell{App: app}
	app.OpenView("recommend")
	tt := ui.NewTester(app.Render, 1200, 800)
	tt.Frame()

	app.mu.Lock()
	if app.ambientActive {
		t.Fatal("ambientActive should be false initially")
	}
	app.mu.Unlock()

	shell.AmbientGlow(AmbientGlowPayload{
		Active: true,
		Left:   [3]uint8{200, 50, 60},
		Top:    [3]uint8{100, 150, 220},
	})

	app.mu.Lock()
	if !app.ambientActive {
		t.Fatal("ambientActive should be true after AmbientGlow notification")
	}
	app.mu.Unlock()

	tt.Frame()

	shell.AmbientGlow(AmbientGlowPayload{Active: false})
	app.mu.Lock()
	if app.ambientActive {
		t.Fatal("ambientActive should be false after deactivating")
	}
	app.mu.Unlock()
	tt.Frame()
}

func TestFeedChannelPillWidthNotCollapsed(t *testing.T) {
	app := NewTestApp()
	app.feedChannel = "推荐"
	app.OpenView("recommend")
	tt := ui.NewTester(app.Render, 1200, 800)
	tt.Frame()
	elem, ok := tt.Find("推荐")
	if !ok {
		t.Fatal("expected 推荐 channel to be found")
	}
	if elem.W < 50 {
		t.Fatalf("selected channel pill width collapsed: width=%v", elem.W)
	}
}
