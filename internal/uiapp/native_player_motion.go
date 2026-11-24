package uiapp

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/playback"
)

// One short page-height slide makes the feed's direction visible. MyGo moves
// the native view's real placement as well as its overlays, clips to the stage,
// and snaps translations under Reduce Motion. Only the outgoing still frame
// crosses the native bridge, once per selection; AVKit keeps decoding video.
var nativeFeedSlide = []ui.Keyframe{
	{Value: 1},
	{At: 260 * time.Millisecond, Value: 0, Ease: ui.CubicBezier(.23, 1, .32, 1)},
}

func nativeFeedOffset(c *ui.Context, s playback.State) float32 {
	if s.Mode != playback.Feed || s.Transition == 0 || s.Status.PictureInPicture || s.Error != "" || len(s.TransitionFrame) == 0 {
		return 0
	}
	// The outgoing still has a bounded lifetime even if source resolution or
	// native display stalls. Its slide reveals the new poster while waiting.
	// Keyframes retarget from their previous sampled value when Run changes.
	// A fresh, disposable clock per selection makes every page start one full
	// height away; the AVKit element itself retains its identity (including PiP).
	clock := ui.Box(c.Key(fmt.Sprintf("feed-slide-clock:%d:%d", s.Scene, s.TransitionID))).Absolute().Size(0, 0).PassThrough()
	return clock.Keyframes("page", nativeFeedSlide)
}

func (a *App) renderNativePlayerVideo(c *ui.Context, s playback.State) {
	stage := ui.Box(c.Key("player-video-pages")).Absolute().Fill().Clip()
	frameKey := fmt.Sprintf("%d:%d", s.Scene, s.TransitionID)
	if a.playerFrameKey != frameKey {
		a.playerFrameKey = frameKey
		a.playerTransitionBitmap = nil
		if len(s.TransitionFrame) > 0 {
			a.playerTransitionBitmap, _ = ui.DecodeBitmap(s.TransitionFrame)
		}
	}
	stage.Children(func() {
		offset := nativeFeedOffset(c, s)
		direction := float32(1)
		if s.Transition < 0 {
			direction = -1
		}
		height := stage.Bounds().H
		incomingOffset := offset
		if !s.Status.Ready || !s.Status.Displayed {
			// Keep AVKit attached onscreen so it can become displayed. Until
			// then the poster stays in place as the outgoing still slides away.
			incomingOffset = 0
		}
		incoming := ui.Box(c.Key("player-incoming-page")).Absolute().Fill().Background(ui.RGB(0, 0, 0)).Translate(0, direction*incomingOffset*height)
		incoming.Children(func() {
			if s.Engine != nil {
				// Widget verdict (MyGo v0.9.1): Windows uses official ui.Video;
				// macOS needs the AVKit ui.NativeView for native preroll and PiP.
				if video, ok := s.Engine.(interface{ VideoPlayer() *ui.VideoPlayer }); ok {
					if p := video.VideoPlayer(); p != nil {
						fit := ui.Contain
						if s.Preferences.Fit == "cover" {
							fit = ui.Cover
						}
						ui.Video(c.Key("system-video-surface"), p).Absolute().Fill().Fit(fit).PassThrough()
					}
				} else {
					ui.NativeView(c.Key("avkit-surface"), s.Engine).Absolute().Fill().PassThrough()
				}
			}
			if nativePlayerPosterVisible(s) {
				if poster := a.Artwork(s.Book.CoverURL); poster != nil {
					ui.Image(c.Key("player-poster"), poster).Absolute().Fill().Fit(ui.Contain).PassThrough()
				}
			}
		})
		if offset > 0 && a.playerTransitionBitmap != nil {
			outgoing := ui.Box(c.Key("player-outgoing-page")).Absolute().Fill().Background(ui.RGB(0, 0, 0)).Translate(0, -direction*(1-offset)*height).PassThrough()
			outgoing.Children(func() {
				fit := ui.Contain
				if s.Preferences.Fit == "cover" {
					fit = ui.Cover
				}
				ui.Image(c.Key("player-outgoing-frame"), a.playerTransitionBitmap).Absolute().Fill().Fit(fit)
			})
		}
	})
}

func nativePlayerPosterVisible(s playback.State) bool {
	return s.Error != "" || (!s.HasDisplayedFrame && (!s.Status.Ready || !s.Status.Displayed))
}
