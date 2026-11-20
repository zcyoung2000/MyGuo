package uiapp

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/playback"
)

// Widget decisions (MyGo v0.9.1): NativeView hosts AVKit; SliderBase owns
// seek/volume semantics; ButtonBase owns actions; List virtualizes episodes
// and comments. Danmaku is tier-3 painting because no widget assigns moving
// timed text to collision-free lanes. Draw callbacks only paint snapshots.
func (a *App) renderNativePlayer(c *ui.Context) {
	s := a.media.Snapshot()
	a.renderNativePlayerState(c, s)
}
func (a *App) renderNativePlayerState(c *ui.Context, s playback.State) {
	// Recommendation playback keeps its chrome visible; the surface gesture
	// still routes through the controller so an open panel can close first.
	if s.Mode == playback.Feed {
		s.Preferences.Clear = false
	}
	a.playerNativeBackdrop = s.Engine != nil && s.Status.Ready && s.Status.Displayed && s.Error == "" && !s.Status.PictureInPicture
	host := a.media
	command := func(cmd string, value any) {
		if host != nil {
			host.PlayerControl(cmd, value)
		}
	}
	if s.SentSequence != 0 && s.SentSequence != a.playerSentSequence {
		a.playerSentSequence = s.SentSequence
		if a.playerDraft == s.SentText {
			a.playerDraft = ""
		}
	}
	clear := s.Preferences.Clear
	editing := false
	viewport, _ := c.Size()
	sidebar := a.sidebarWidth
	if sidebar == 0 {
		sidebar = defaultSidebarWidth
	}
	if a.playerFullscreen {
		sidebar = 0
	}
	available := max(float32(320), viewport-sidebar)
	drawerWidth := min(float32(320), available-36)
	ui.Box(c.Key("native-player")).Label("原生播放器").Fill().MinWidth(0).MinHeight(0).Clip().Background(ui.RGB(0, 0, 0)).Children(func() {
		stage := ui.Box(c.Key("player-stage")).Absolute().Fill().Clip()
		stage.Children(func() {
			a.renderNativePlayerVideo(c, s)
			ui.ButtonBase(c.Key("player-video-toggle")).Absolute().Fill().Label("播放画面").Description(playerSurfaceDescription(s)).TabStop(false).Background(ui.Transparent).OnClick(func() {
				if c.KeyDown(ui.KeySpace) || c.KeyDown(ui.KeyEnter) {
					command("toggle", nil)
				} else {
					command("surface", nil)
				}
			})
			if s.Preferences.Danmaku.Enabled && s.Status.Ready {
				a.renderNativeDanmaku(c, s)
			}
			// Recovery stays in the controller/logs; failures never cover the
			// picture with an error card or a reload button.
		})
		if !clear {
			dock := ui.Column(c.Key("player-controls-overlay")).Absolute().Left(18).Right(18).Bottom(16).MinWidth(0).PassThrough().Transition(playerChromeTransition)
			dock.Children(func() { editing = a.renderNativePlayerControls(c, s, command) })
			titleBottom := float32(148)
			if dock.Bounds().H > 0 {
				titleBottom = dock.Bounds().H + 32
			} else {
				// Settle the measured title anchor before its first click, rather
				// than moving the link between pointer down and pointer up.
				c.Invalidate()
			}
			a.renderNativePlayerHeading(c, s, titleBottom, available, command)
			if s.Panel == "" {
				a.renderNativePlayerRail(c, s, command)
			}
			if s.Panel != "" {
				a.renderNativePlayerPanel(c, s, drawerWidth, command)
			}
		}
		if s.Paused && s.Status.Ready && !s.Loading {
			ui.Text(c.Key("player-paused"), "已暂停").Absolute().Left(24).Top(84).Padding(6, 10).Radius(12).Background(ui.RGBA(0, 0, 0, .3)).TextColor(ui.RGBA(255, 255, 255, .8)).PassThrough()
		}
		if s.Notice != "" {
			ui.Row(c.Key("player-notice")).Absolute().Top(86).Left(18).Right(18).Justify(ui.Center).Children(func() {
				contentSurface(ui.Row(c).Padding(10, 14).Gap(12).Radius(14)).Children(func() {
					ui.Text(c, s.Notice).FontSize(13).TextColor(ui.RGB(255, 255, 255)).MaxWidth(420)
					panelAction(c, "player-dismiss-notice", "关闭", func() { command("dismiss", nil) })
				})
			})
		}
	})
	if s.Mode == playback.Feed {
		a.feedChrome = FeedChrome{Visible: !clear}
		a.feedChromeKnown = true
	}
	if s.Panel == "" && !editing {
		for _, binding := range []struct {
			key     ui.Key
			command string
			value   any
		}{{ui.KeySpace, "toggle", nil}, {ui.KeyDown, "next", nil}, {ui.KeyUp, "prev", nil}, {ui.KeyLeft, "seekBy", -5}, {ui.KeyRight, "seekBy", 5}, {ui.KeyF, "fullscreen", nil}, {ui.KeyP, "pip", nil}, {ui.KeyB, "danmaku", nil}, {ui.KeyEscape, "close", nil}} {
			if c.Shortcut(0, binding.key) {
				if !c.ShortcutRepeated() || binding.command == "seekBy" {
					if binding.key == ui.KeyEscape && clear {
						command("clear", false)
					} else if binding.key == ui.KeyEscape && a.playerFullscreen {
						command("fullscreen", nil)
					} else {
						command(binding.command, binding.value)
					}
				}
			}
		}
	} else if s.Panel != "" && c.Shortcut(0, ui.KeyEscape) {
		command("panel", s.Panel)
	}

}

func playerSurfaceDescription(s playback.State) string {
	if s.Panel != "" {
		return "点击收起面板"
	}
	if s.Error != "" {
		return "点击继续播放"
	}
	if s.Mode == playback.Feed {
		return "点击暂停"
	}
	if s.Paused || s.Status.Ended {
		return "点击继续播放并收起控件"
	}
	if s.Preferences.Clear {
		return "点击暂停并显示控件"
	}
	return "点击收起控件，继续播放"
}
func chooseText(value bool, yes, no string) string {
	if value {
		return yes
	}
	return no
}
func (a *App) playerAction(c *ui.Context, key, label string, action func()) ui.Element {
	return a.chromeButton(c, key, label).OnClick(action)
}
func (a *App) nativeRailAction(c *ui.Context, key string, icon *ui.SVG, label string, active bool, action func()) {
	b := ui.ButtonBase(c.Key(key)).Label(label).Focusable().FillWidth().Radius(10).Padding(8, 2).OnClick(action)
	hoverBackground(b, ui.Transparent, ui.RGBA(255, 255, 255, .08))
	tint := ui.RGBA(255, 255, 255, .85)
	if active {
		tint = ui.Hex("#fe2c55")
	}
	b.Children(func() {
		ui.Column(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon).Size(24, 24).TextColor(tint)
			ui.Text(c, label).FontSize(11).SingleLine().TextColor(tint)
		})
	})
}
func (a *App) renderNativePlayerControls(c *ui.Context, s playback.State, command func(string, any)) bool {
	focused := false
	ui.Column(c.Key("player-controls")).Gap(8).MinWidth(0).PassThrough().Children(func() {
		position := s.Status.Position
		duration := s.Status.Duration
		if duration <= 0 && s.Playback != nil {
			duration = float64(s.Playback.DurationSec)
		}
		playerSlider(c, "player-seek", "播放进度", position, math.Max(1, duration), s.Status.Buffered, func(v float64) { command("seek", v) }).FillWidth()
		ui.Row(c.Key("player-controls-row")).Wrap().Gap(6).AlignItems(ui.Center).PassThrough().Children(func() {
			a.playerAction(c, "player-play", chooseText(s.Paused || s.Status.Ended || s.Error != "", "播放", "暂停"), func() { command("toggle", nil) })
			a.playerAction(c, "player-prev-episode", "上一集", func() { command("prevEpisode", nil) })
			a.playerAction(c, "player-next-episode", "下一集", func() { command("nextEpisode", nil) })
			ui.Text(c.Key("player-time"), playerTime(position)+" / "+playerTime(duration)).FontSize(11).TextColor(ui.RGBA(255, 255, 255, .66)).SingleLine().PassThrough()
			ui.Box(c).Grow(1).PassThrough()
			quality := s.Preferences.Quality
			if s.Playback != nil && s.Playback.Quality != "" {
				quality = s.Playback.Quality
			}
			a.choiceMenu(c, "player-quality", quality, func(m *ui.Menu) {
				if s.Playback == nil || len(s.Playback.Qualities) == 0 {
					m.Item("视频加载后可选择画质").Disabled(true)
					return
				}
				for _, q := range s.Playback.Qualities {
					label := q.Label
					if label == "" {
						label = q.ID
					}
					if m.Item(label).ID(q.ID).Checked(q.ID == quality).Disabled(!q.Available).Chosen() {
						command("quality", q.ID)
					}
				}
			})
			a.choiceMenu(c, "player-rate", fmt.Sprintf("%g×", s.Preferences.Rate), func(m *ui.Menu) {
				for _, rate := range []float64{.75, 1, 1.25, 1.5, 2} {
					if m.Item(fmt.Sprintf("%g×", rate)).Checked(rate == s.Preferences.Rate).Chosen() {
						command("speed", rate)
					}
				}
			})
			a.playerAction(c, "player-pip", "画中画", func() { command("pip", nil) })
			a.playerAction(c, "player-fullscreen", "全屏", func() { command("fullscreen", nil) })
			a.playerAction(c, "player-settings", "设置", func() { command("panel", "settings") })
		})
		ui.Row(c.Key("player-danmaku-row")).Gap(8).AlignItems(ui.Center).MinWidth(0).PassThrough().Children(func() {
			a.playerAction(c, "player-danmaku-toggle", chooseText(s.Preferences.Danmaku.Enabled, "弹幕开", "弹幕关"), func() { command("danmaku", nil) })
			draft := a.playerDraft
			input := a.chromeSurface(ui.TextInput(c.Key("player-danmaku-input"), &draft).Grow(1).MinWidth(64).Height(32).Padding(6, 10).Radius(16)).Placeholder("发一条弹幕，一起看剧").Label("弹幕内容").Disabled(s.Sending).OnChange(func() { a.mu.Lock(); a.playerDraft = draft; a.mu.Unlock() })
			focused = input.FocusWithin()
			send := func() {
				if a.media != nil && a.media.controller != nil {
					a.media.controller.SendDanmaku(draft, "")
				}
			}
			input.OnTextKey(func(k ui.TextKey) bool {
				if k.Key != ui.KeyEnter || k.Mods != 0 {
					return false
				}
				if !s.Sending && a.media != nil && a.media.controller != nil {
					a.media.controller.SendDanmaku(k.Text(), "")
				}
				return true
			})
			a.playerAction(c, "player-danmaku-send", chooseText(s.Sending, "发送中", "发送"), send).Disabled(s.Sending)
			a.playerAction(c, "player-danmaku-settings", "弹幕设置", func() { command("panel", "danmaku") })
		})
	})
	return focused
}
func playerSlider(c *ui.Context, key, label string, value, maximum, buffered float64, changed func(float64)) ui.Element {
	current := math.Max(0, math.Min(maximum, value))
	slider := ui.SliderBase(c.Key(key), &current, 0, maximum).Height(22).Padding(0, 5).Label(label).OnChange(func() { changed(current) })
	fraction := float32(current / maximum)
	bufferFraction := float32(math.Min(1, math.Max(0, buffered/maximum)))
	slider.Draw(func(p *ui.Painter, r ui.Rect) {
		track := ui.Rect{X: r.X + 5, Y: r.Y + r.H/2 - 2, W: r.W - 10, H: 4}
		p.Fill(track, ui.RGBA(255, 255, 255, .17), 2)
		buffer := track
		buffer.W *= bufferFraction
		p.Fill(buffer, ui.RGBA(255, 255, 255, .3), 2)
		played := track
		played.W *= fraction
		p.Fill(played, ui.Hex("#fe2c55"), 2)
		p.Fill(ui.Rect{X: track.X + track.W*fraction - 4, Y: track.Y - 2, W: 8, H: 8}, ui.RGB(255, 255, 255), 4)
	})
	return slider
}
func playerTime(seconds float64) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		seconds = 0
	}
	n := int(seconds)
	if n >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", n/3600, n/60%60, n%60)
	}
	return fmt.Sprintf("%02d:%02d", n/60, n%60)
}

// Position extrapolation gives danmaku smooth movement between AVPlayer samples.
// Paused or buffering clocks stay still, including system PiP pause actions.
func nativePlayerPosition(s playback.State, now time.Time) float64 {
	position := s.Status.Position
	if !s.Paused && s.Status.Playing && !s.Status.Buffering && !s.Updated.IsZero() {
		position += math.Min(.3, math.Max(0, now.Sub(s.Updated).Seconds())) * s.Preferences.Rate
	}
	return position
}
