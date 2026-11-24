package uiapp

import (
	"bytes"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"myguo/internal/ipc"
	"myguo/internal/playback"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativePlayerLayoutAndPanelMatrix(t *testing.T) {
	directory := os.Getenv("MYGUO_PLAYER_SCREENSHOT_DIR")
	for _, size := range [][2]int{{1000, 640}, {1280, 820}, {1600, 1000}} {
		for _, panel := range []string{"", "episodes", "comments", "settings", "danmaku"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], panel), func(t *testing.T) {
				player := newNativeUIPlayer()
				player.state.Panel = panel
				player.state.CommentItems = []ipc.CommentDTO{{ID: "comment", Author: "剧友", Content: "原生评论长文本验收，连续查看与滚动时保持布局，不覆盖视频和控件。", ReplyCount: 2}}
				player.state.CommentHasMore = true
				app := NewTestApp()
				app.SetMediaHost(&MediaHost{controller: player})
				app.OpenView("recommend")
				tt := ui.NewTester(app.Render, size[0], size[1])
				tt.Advance(300 * time.Millisecond)
				stage, ok := tt.Find("播放画面")
				if !ok || stage.W < 220 || math.Abs(float64(stage.Y)) > 1 || math.Abs(float64(stage.Y+stage.H-float32(size[1]))) > 1 || math.Abs(float64(stage.X+stage.W-float32(size[0]))) > 1 {
					t.Fatalf("video squeezed: %+v", stage)
				}
				var rowTop, rowBottom float32
				for i, label := range []string{"播放进度", "弹幕设置", "弹幕开", "全屏", "画中画"} {
					rect, ok := tt.Find(label)
					if !ok || rect.W <= 0 || rect.H <= 0 || rect.X < 0 || rect.Y < 0 || rect.X+rect.W > float32(size[0])+1 || rect.Y+rect.H > float32(size[1])+1 {
						t.Fatalf("control %q clipped: %+v", label, rect)
					}
					if label != "播放进度" {
						if i == 1 {
							rowTop, rowBottom = rect.Y, rect.Y+rect.H
						} else if rect.Y < rowTop || rect.Y+rect.H > rowBottom {
							t.Fatalf("control %q left the single control row: rect=%+v row=[%g,%g]", label, rect, rowTop, rowBottom)
						}
					}
				}
				for _, gone := range []string{"弹幕内容", "发送"} {
					if _, ok := tt.Find(gone); ok {
						t.Fatalf("danmaku sending control %q remains", gone)
					}
				}
				if _, ok := tt.AccessByRole(ui.RoleButton, "刷新"); ok {
					t.Fatal("player refresh button remains")
				}
				if panel != "" {
					close, ok := tt.Find("收起")
					if !ok || close.X < stage.X || close.X+close.W > stage.X+stage.W || close.Y <= stage.Y {
						t.Fatalf("drawer must float within the unchanged video stage: close=%+v stage=%+v", close, stage)
					}
				}
				if directory != "" {
					if err := os.MkdirAll(directory, 0700); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(directory, fmt.Sprintf("player-%dx%d-%s.png", size[0], size[1], panel)))
					if err != nil {
						t.Fatal(err)
					}
					if err = png.Encode(file, tt.Image()); err != nil {
						t.Fatal(err)
					}
					file.Close()
				}
			})
		}
	}
}
func TestNativeDanmakuLanesAvoidCatchupAndSeekClockStaysStillWhenPaused(t *testing.T) {
	if danmakuLaneAvailable(danmakuFlight{start: 0, width: 100, speed: 400.0 / 9}, danmakuFlight{start: 1, width: 240, speed: 540.0 / 9}, 300) {
		t.Fatal("a faster long item catches the preceding item")
	}
	if !danmakuLaneAvailable(danmakuFlight{start: 0, width: 100, speed: 400.0 / 9}, danmakuFlight{start: 9, width: 240, speed: 540.0 / 9}, 300) {
		t.Fatal("empty lane not reused")
	}
	now := time.Now()
	s := playback.State{Paused: true, Updated: now.Add(-time.Second), Preferences: playback.DefaultPreferences()}
	s.Status.Position = 27
	s.Status.Playing = true
	if got := nativePlayerPosition(s, now); got != 27 {
		t.Fatalf("paused danmaku moved to %g", got)
	}
	s.Paused = false
	s.Updated = now.Add(-100 * time.Millisecond)
	s.Preferences.Rate = 1.5
	if got := nativePlayerPosition(s, now); math.Abs(got-27.15) > .001 {
		t.Fatalf("rate clock not preserved: %g", got)
	}
}

func TestNativePlayerShortcutsStillReachPlayback(t *testing.T) {
	player := newNativeUIPlayer()
	app := NewTestApp()
	app.SetMediaHost(&MediaHost{controller: player})
	app.OpenView("recommend")
	tt := ui.NewTester(app.Render, 1280, 820)
	tt.Advance(300 * time.Millisecond)
	tt.Key(0, ui.KeyB)
	tt.Key(0, ui.KeyF)
	if len(player.commands) < 2 || player.commands[len(player.commands)-2] != "danmaku" || player.commands[len(player.commands)-1] != "fullscreen" {
		t.Fatalf("playback shortcuts lost: %v", player.commands)
	}
}

func TestNativeDanmakuHoverOffersActionsAndKeepsVideoClickAvailable(t *testing.T) {
	p := newNativeUIPlayer()
	p.state.Paused = true
	p.state.Status.Playing = false
	p.state.DanmakuItems = []ipc.Danmaku{{ID: "hover", Text: "原生悬停互动", TimeMs: 46000, ShowDigg: true}}
	app := NewTestApp()
	app.SetMediaHost(&MediaHost{controller: p})
	app.OpenView("recommend")
	tt := ui.NewTester(app.Render, 1280, 820)
	tt.Advance(300 * time.Millisecond)
	stage, ok := tt.Find("播放画面")
	if !ok {
		t.Fatal("missing video")
	}
	video := nativeDanmakuRect(stage, p.state)
	layout := buildDanmakuLayout("fixture", p.state.DanmakuItems, video.W, video.H, p.state.Preferences.Danmaku)
	f := layout.flights[0]
	x := video.X + video.W - float32(2*f.speed) + 8
	y := video.Y + 10 + layout.lineHeight/2
	tt.Move(x, y)
	tt.Advance(20 * time.Millisecond)
	if _, ok := tt.Find("+1"); ok {
		t.Fatal("danmaku sending action remains")
	}
	tt.Move(stage.X+10, stage.Y+stage.H-20)
	tt.Advance(20 * time.Millisecond)
	if _, ok := tt.Find("+1"); ok {
		t.Fatal("hover controls remain over unrelated video")
	}
	tt.ClickAt(stage.X+10, stage.Y+stage.H-20)
	if len(p.commands) == 0 || p.commands[len(p.commands)-1] != "surface" {
		t.Fatal("danmaku blocks background pause")
	}
}

func TestNativeSeekSliderUsesThePositionClicked(t *testing.T) {
	p := newNativeUIPlayer()
	p.state.Paused = true
	p.state.Status.Playing = false
	app := NewTestApp()
	app.SetMediaHost(&MediaHost{controller: p})
	app.OpenView("recommend")
	tt := ui.NewTester(app.Render, 1280, 820)
	r, ok := tt.Find("播放进度")
	if !ok {
		t.Fatal("missing seek control")
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	if got, ok := p.values["seek"].(float64); !ok || math.Abs(got-100) > 1 {
		t.Fatalf("seek clicked halfway produced %v", p.values["seek"])
	}
}

func TestNativeDrawerKeepsVideoUsableWithWideSidebar(t *testing.T) {
	p := newNativeUIPlayer()
	p.state.Panel = "comments"
	app := NewTestApp()
	app.sidebarWidth = 420
	app.SetMediaHost(&MediaHost{controller: p})
	app.OpenView("recommend")
	tt := ui.NewTester(app.Render, 1000, 640)
	tt.Advance(300 * time.Millisecond)
	stage, ok := tt.Find("播放画面")
	if !ok || stage.W < 250 {
		t.Fatalf("wide sidebar squeezes native video: %+v", stage)
	}
	if app.sidebarWidth != 420 {
		t.Fatal("temporary layout overwrote sidebar preference")
	}
	if err := tt.Click("收起"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	divider, ok := tt.Find("Divider")
	if !ok || divider.X < 410 {
		t.Fatalf("sidebar preference not restored: %+v", divider)
	}
}

func TestNativeFeedSlidesEverySelectionAndHonorsReducedMotion(t *testing.T) {
	s := newNativeUIPlayer().state
	s.Scene = 1
	s.TransitionFrame = []byte("outgoing")
	var offset float32
	tt := ui.NewTester(func(c *ui.Context) {
		offset = nativeFeedOffset(c, s)
		ui.Box(c).Fill()
	}, 800, 600)
	for _, direction := range []int{1, 1, -1, 1} {
		s.Transition = direction
		s.TransitionID++
		tt.Frame()
		if offset < .99 {
			t.Fatalf("selection %d starts at %g, missing full-height slide", s.TransitionID, offset)
		}
		tt.Advance(100 * time.Millisecond)
		if offset <= 0 || offset >= 1 {
			t.Fatalf("slide did not advance: %g", offset)
		}
		tt.Advance(200 * time.Millisecond)
		if offset != 0 || tt.FramePending() {
			t.Fatalf("slide did not settle: offset=%g pending=%v", offset, tt.FramePending())
		}
	}
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	s.TransitionID++
	tt.Frame()
	if offset != 0 || tt.FramePending() {
		t.Fatal("Reduce Motion retained the page translation")
	}
}

func TestNativeFeedSlideSettlesWhileWaitingForDisplayedFrame(t *testing.T) {
	s := newNativeUIPlayer().state
	s.TransitionFrame = []byte("outgoing")
	var offset float32
	tt := ui.NewTester(func(c *ui.Context) { offset = nativeFeedOffset(c, s); ui.Box(c).Fill() }, 800, 600)
	s.Transition = 1
	s.TransitionID = 1
	s.Status.Displayed = false
	tt.Frame()
	if offset < .99 {
		t.Fatal("outgoing frame did not start its slide")
	}
	tt.Advance(time.Second)
	if offset != 0 || tt.FramePending() {
		t.Fatal("slow native display held the old picture indefinitely")
	}
	s.Status.Displayed = true
	tt.Frame()
	if offset != 0 {
		t.Fatal("late native frame replayed the completed slide")
	}
}

func TestNativeFeedFailureRevealsCurrentPoster(t *testing.T) {
	poster := image.NewRGBA(image.Rect(0, 0, 80, 60))
	draw.Draw(poster, poster.Bounds(), image.NewUniform(color.RGBA{G: 255, A: 255}), image.Point{}, draw.Src)
	frame := image.NewRGBA(poster.Bounds())
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.RGBA{R: 255, A: 255}), image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, frame); err != nil {
		t.Fatal(err)
	}
	a := NewTestApp()
	a.artwork = newArtworkLoader("", nil, nil)
	a.artwork.entries["poster"] = artworkEntry{bitmap: ui.NewBitmap(poster)}
	s := newNativeUIPlayer().state
	s.Book.CoverURL = "poster"
	s.Transition, s.TransitionID = 1, 1
	s.TransitionFrame = encoded.Bytes()
	s.Status.Ready, s.Status.Displayed = false, false
	s.Loading = true
	tt := ui.NewTester(func(c *ui.Context) { a.renderNativePlayerVideo(c, s) }, 800, 600)
	tt.Advance(300 * time.Millisecond)
	if pixel := tt.Image().RGBAAt(400, 300); pixel.G < 250 || pixel.R > 5 {
		t.Fatalf("loading item remained covered by the old picture: %+v", pixel)
	}
	s.TransitionID++
	s.Error = "视频暂时无法加载，请稍后重试"
	tt.Frame()
	if pixel := tt.Image().RGBAAt(400, 300); pixel.G < 250 || pixel.R > 5 {
		t.Fatalf("failed item remained covered by the old picture: %+v", pixel)
	}
}

func TestDanmakuUsesBlackMarginsAndReservesControls(t *testing.T) {
	s := newNativeUIPlayer().state
	stage := ui.Rect{W: 1069, H: 820}
	picture := nativeVideoRect(stage, s)
	area := nativeDanmakuRect(stage, s)
	if area.X >= picture.X || area.W <= picture.W {
		t.Fatal("portrait danmaku is confined to the video picture")
	}
	if area.Y < 70 || area.Y+area.H > stage.H-140 || area.X != stage.X || area.W != stage.W {
		t.Fatalf("danmaku must keep vertical control clearance and travel edge to edge: %+v", area)
	}
	s.Panel = "comments"
	if opened := nativeDanmakuRect(stage, s); opened != area {
		t.Fatalf("opening a drawer moved the danmaku entrance: %+v", opened)
	}
	s.Preferences.Clear = true
	clear := nativeDanmakuRect(stage, s)
	if clear.W != area.W || clear.H <= area.H {
		t.Fatal("clear-screen mode did not restore the canvas")
	}
}

func TestNativeDanmakuToggleAndSettingsStayOnTheSingleControlRow(t *testing.T) {
	player := newNativeUIPlayer()
	player.state.Preferences.Danmaku.Enabled = true
	app := NewTestApp()
	app.SetMediaHost(&MediaHost{controller: player})
	app.OpenView("recommend")
	tt := ui.NewTester(app.Render, 1280, 820)
	tt.Advance(300 * time.Millisecond)
	if _, ok := tt.Find("弹幕开"); !ok {
		t.Fatal("danmaku toggle missing")
	}
	if err := tt.Click("弹幕开"); err != nil {
		t.Fatal(err)
	}
	if len(player.commands) == 0 || player.commands[len(player.commands)-1] != "danmaku" {
		t.Fatalf("danmaku toggle did not reach the controller: %v", player.commands)
	}
	if err := tt.Click("弹幕设置"); err != nil {
		t.Fatal(err)
	}
	if player.state.Panel != "danmaku" {
		t.Fatalf("danmaku settings did not open: %q", player.state.Panel)
	}
	if _, ok := tt.Find("显示弹幕"); !ok {
		t.Fatal("danmaku settings panel content missing")
	}
}
