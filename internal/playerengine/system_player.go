package playerengine

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"myguo/internal/avkit"
)

// systemVideo isolates the main-thread-only MyGo API from the controller actor.
type systemVideo interface {
	play()
	pause()
	seek(float64)
	configure(float64, float64, bool)
	snapshot() avkit.Status
	player() *ui.VideoPlayer
	close()
}

type pipWindow interface {
	Close()
	Invalidate()
	OnClosed(func()) func()
	Show()
}

// All mutable fields belong to the UI thread, including Load and Close. Never
// hold an actor lock while waiting for it; application shutdown keeps the UI
// loop running until the controller has saved its clock and closed its players.
type systemPlayer struct {
	open           func(string) (systemVideo, error)
	dispatch       func(func())
	after          func(time.Duration, func()) *time.Timer
	video          systemVideo
	pip            pipWindow
	pipWakeTimer   *time.Timer
	pipWakeVersion uint64
	closed         bool
	rate           float64
	volume         float64
	muted          bool
	seekPending    bool
	seekTarget     float64
	playbackIntent func(bool)
}

const (
	pipFrameWakeInterval = 250 * time.Millisecond
	pipWindowWidth       = 400
	pipWindowHeight      = 225
	pipWindowMinWidth    = 240
	pipWindowMinHeight   = 160
)

func newSystemPlayer(open func(string) (systemVideo, error), dispatch func(func())) *systemPlayer {
	return &systemPlayer{open: open, dispatch: dispatch, after: time.AfterFunc, rate: 1, volume: 1}
}

func (p *systemPlayer) stopPiPFrameWake() {
	p.pipWakeVersion++
	if p.pipWakeTimer != nil {
		p.pipWakeTimer.Stop()
		p.pipWakeTimer = nil
	}
}

func (p *systemPlayer) wakePiPFrame(version uint64) {
	if p.closed || p.pip == nil || p.video == nil || p.pipWakeVersion != version {
		return
	}
	status := p.video.snapshot()
	if status.Ready || status.Error != "" {
		p.pipWakeTimer = nil
		return
	}
	p.pip.Invalidate()
	p.pipWakeTimer = p.after(pipFrameWakeInterval, func() {
		p.dispatch(func() { p.wakePiPFrame(version) })
	})
}

func (p *systemPlayer) beginPiPFrameWake() {
	p.stopPiPFrameWake()
	if p.pip == nil || p.video == nil || p.closed {
		return
	}
	version := p.pipWakeVersion
	p.pipWakeTimer = p.after(pipFrameWakeInterval, func() {
		p.dispatch(func() { p.wakePiPFrame(version) })
	})
}

func (p *systemPlayer) NativeView() uintptr { return 0 }

// VideoPlayer is an optional renderer capability. A video belongs to one
// window at a time; while PiP owns it the main view shows its poster instead.
func (p *systemPlayer) VideoPlayer() (out *ui.VideoPlayer) {
	p.dispatch(func() {
		if !p.closed && p.video != nil && p.pip == nil {
			out = p.video.player()
		}
	})
	return
}

func (p *systemPlayer) Load(url string) (err error) {
	err = errors.New("player is closed")
	p.dispatch(func() {
		if p.closed {
			return
		}
		p.stopPiPFrameWake()
		if p.video != nil {
			p.video.close()
			p.video = nil
		}
		p.seekPending = false
		p.video, err = p.open(url)
		if err == nil {
			// Prevent autoplay before the controller has applied playback intent,
			// also when a background candidate was opened for source warming.
			p.video.pause()
			p.video.configure(p.rate, p.volume, p.muted)
		}
		if p.pip != nil {
			p.pip.Invalidate()
			if err == nil {
				p.beginPiPFrameWake()
			}
		}
	})
	return
}

func (p *systemPlayer) withVideo(fn func(systemVideo)) {
	p.dispatch(func() {
		if !p.closed && p.video != nil {
			fn(p.video)
		}
	})
}

func (p *systemPlayer) Play()  { p.withVideo(systemVideo.play) }
func (p *systemPlayer) Pause() { p.withVideo(systemVideo.pause) }
func (p *systemPlayer) Seek(seconds float64) {
	if !math.IsNaN(seconds) && !math.IsInf(seconds, 0) {
		p.withVideo(func(v systemVideo) {
			p.seekTarget = math.Max(0, seconds)
			p.seekPending = !v.snapshot().Ready
			if !p.seekPending {
				v.seek(p.seekTarget)
			}
		})
	}
}

// Without published seekable ranges, never jump beyond a growing playlist's
// produced prefix. The controller uses its complete local MP4 for seeks.
func (p *systemPlayer) SeekRequiresCompleteSource() bool { return true }

// PiP buttons update the actor's intent immediately. Polling the window's
// status alone can lose a pause when the user closes PiP before the next tick.
func (p *systemPlayer) SetPlaybackIntentHandler(handler func(bool)) {
	p.dispatch(func() { p.playbackIntent = handler })
}

func (p *systemPlayer) togglePiPPlayback() {
	if p.closed || p.video == nil {
		return
	}
	paused := p.video.snapshot().Playing
	if paused {
		p.video.pause()
	} else {
		p.video.play()
	}
	if p.playbackIntent != nil {
		p.playbackIntent(paused)
	}
}

func renderPiPControls(c *ui.Context, playing bool, togglePlayback, returnToMain func()) {
	playbackLabel := "继续播放"
	playbackGlyph := "▶"
	if playing {
		playbackLabel = "暂停播放"
		playbackGlyph = "Ⅱ"
	}
	ui.Row(c.Key("pip-controls")).Absolute().Right(12).Bottom(12).Gap(6).
		Padding(5).Radius(999).Background(ui.RGBA(18, 18, 18, .92)).Children(func() {
		ui.Button(c, playbackGlyph).Label(playbackLabel).Tooltip(playbackLabel).
			Size(40, 36).Radius(999).OnClick(togglePlayback)
		ui.Button(c, "×").Label("返回主窗口").Tooltip("返回主窗口").
			Size(40, 36).Radius(999).OnClick(returnToMain)
	})
}

func (p *systemPlayer) Configure(rate, volume float64, muted bool, fit string) {
	p.dispatch(func() {
		p.rate, p.volume, p.muted = rate, volume, muted
		if !p.closed && p.video != nil {
			p.video.configure(rate, volume, muted)
		}
	})
}
func (p *systemPlayer) Snapshot() (s avkit.Status) {
	p.dispatch(func() {
		if !p.closed && p.video != nil {
			s = p.video.snapshot()
			if p.seekPending && s.Ready {
				p.video.seek(p.seekTarget)
				p.seekPending = false
				s = p.video.snapshot()
			}
			s.PictureInPicture = p.pip != nil
			s.Displayed = s.Displayed && p.pip == nil
		}
	})
	return
}

// Opening a source warms it, but MyGo only publishes readiness when ui.Video
// displays a decoded frame. Do not secretly play an unattached neighbor.
func (p *systemPlayer) Preroll()             { p.Pause() }
func (p *systemPlayer) CaptureFrame() []byte { return nil }

func (p *systemPlayer) TogglePictureInPicture(ctx context.Context) (err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	p.dispatch(func() {
		if err = ctx.Err(); err != nil {
			return
		}
		if p.pip != nil {
			p.pip.Close()
			return
		}
		if p.closed || p.video == nil || !p.video.snapshot().Ready {
			err = errors.New("video has no frame yet")
			return
		}
		// Widget verdict (MyGo v0.9.1): official ui.Video and ui.Button.
		// There is no public system PiP API; a resizable, always-on-top native
		// window hosts the same player, preserving its source and clock.
		origin := mygo.FocusedWindow()
		win := mygo.NewWindow(mygo.WindowOptions{
			Title: "myguo · 画中画", Width: pipWindowWidth, Height: pipWindowHeight,
			MinWidth: pipWindowMinWidth, MinHeight: pipWindowMinHeight, AlwaysOnTop: true,
			SkipTaskbar: true, DisableMinimize: true, DisableMaximize: true,
			BackgroundColor: "#000000", Hidden: true,
			Content: ui.View(func(c *ui.Context) {
				ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Children(func() {
					if p.video != nil && !p.closed {
						ui.Video(c.Key("pip-video"), p.video.player()).Fill().Fit(ui.Contain)
						renderPiPControls(c, p.video.snapshot().Playing, p.togglePiPPlayback, func() {
							if p.pip != nil {
								p.pip.Close()
							}
						})
					}
				})
			}),
		})
		p.pip = win
		win.OnClosed(func() {
			if p.pip == win {
				p.stopPiPFrameWake()
				p.pip = nil
			}
			if !p.closed && origin != nil {
				origin.Show()
				origin.Restore()
				origin.Focus()
			}
		})
		win.Show()
	})
	return
}

func (p *systemPlayer) Close() error {
	p.dispatch(func() {
		if p.closed {
			return
		}
		p.stopPiPFrameWake()
		p.closed = true
		p.playbackIntent = nil
		if p.pip != nil {
			p.pip.Close()
			p.pip = nil
		}
		if p.video != nil {
			p.video.close()
			p.video = nil
		}
	})
	return nil
}
