package uiapp

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"math"
	"myguo/internal/ipc"
	"myguo/internal/playback"
	"sort"
)

type danmakuFlight struct {
	item         ipc.Danmaku
	glyphs       []ui.Glyph
	width        float32
	start, speed float64
	lane         int
}
type danmakuHover struct {
	key, id     string
	at          float64
	rect, panel ui.Rect
}

type danmakuLayout struct {
	key                               string
	flights                           []danmakuFlight
	width, height, lineHeight, ascent float32
}

func (a *App) renderNativeDanmaku(c *ui.Context, s playback.State) {
	// Before its first measured layout this transparent canvas must let clicks
	// reach the picture. A blank, unconfigured hit region otherwise eats them.
	layer := ui.Box(c.Key("native-danmaku")).Absolute().Fill().Clip().HitTest(func(float32, float32, ui.Rect) bool { return false })
	bounds := layer.Bounds()
	if bounds.W <= 0 || bounds.H <= 0 {
		c.Invalidate()
		return
	}
	rect := nativeDanmakuRect(bounds, s)
	d := s.Preferences.Danmaku
	episode := ""
	if s.Playback != nil {
		episode = s.Playback.EpisodeID
	}
	key := fmt.Sprintf("%d:%s:%g:%g:%g:%d:%d:%d:%d", s.Scene, episode, rect.W, rect.H, d.FontSize, d.FontWeight, d.Rows, len(s.DanmakuItems), s.DanmakuVersion)
	if a.playerDanmaku.key != key {
		a.playerDanmaku = buildDanmakuLayout(key, s.DanmakuItems, rect.W, rect.H, d)
	}
	layout := a.playerDanmaku
	now := nativePlayerPosition(s, c.Now())
	localVideo := nativeDanmakuRect(ui.Rect{W: bounds.W, H: bounds.H}, s)
	hover := a.playerDanmakuHover
	px, py, over := layer.PointerPosition()
	if !over || hover.key != key || (!pointInRect(px, py, hover.rect) && !pointInRect(px, py, hover.panel)) {
		hover = danmakuHover{}
		if over {
			if flight, rect, ok := hitDanmaku(layout, localVideo, s.DanmakuLaunchAfter, now, px, py); ok {
				panel := ui.Rect{X: max(localVideo.X, min(rect.X, localVideo.X+localVideo.W-180)), Y: rect.Y + rect.H + 2, W: 180, H: 38}
				if panel.Y+panel.H > localVideo.Y+localVideo.H {
					panel.Y = rect.Y - 40
				}
				hover = danmakuHover{key: key, id: flight.item.ID, at: now, rect: ui.Rect{X: rect.X, Y: rect.Y - 2, W: rect.W, H: rect.H + 6}, panel: panel}
			}
		}
	}
	if s.Preferences.Clear {
		hover = danmakuHover{}
	}
	a.playerDanmakuHover = hover
	layer.HitTest(func(x, y float32, r ui.Rect) bool {
		if s.Preferences.Clear {
			return false
		}
		x -= r.X
		y -= r.Y
		if hover.id != "" && (pointInRect(x, y, hover.rect) || pointInRect(x, y, hover.panel)) {
			return true
		}
		_, _, ok := hitDanmaku(layout, nativeDanmakuRect(ui.Rect{W: r.W, H: r.H}, s), s.DanmakuLaunchAfter, now, x, y)
		return ok
	})
	if hover.id != "" && a.media != nil && a.media.controller != nil {
		for _, flight := range layout.flights {
			if flight.item.ID == hover.id {
				item := flight.item
				layer.Children(func() {
					contentSurface(ui.Row(c.Key("danmaku-hover-actions")).Absolute().Left(hover.panel.X).Top(hover.panel.Y).Width(hover.panel.W).Height(hover.panel.H).Padding(3).Gap(4).Radius(8)).Children(func() {
						if item.ShowDigg {
							panelAction(c, "danmaku-hover-like", fmt.Sprintf("%s %d", chooseText(item.Liked, "已赞", "点赞"), item.LikeCount), func() { a.media.controller.LikeDanmaku(item.ID, !item.Liked) })
						}
						if item.ShowAddOne {
							panelAction(c, "danmaku-hover-add", "+1", func() { a.media.controller.SendDanmaku(item.Text, item.ID) })
						}
					})
				})
				break
			}
		}
	}
	layer.Draw(func(p *ui.Painter, r ui.Rect) {
		video := nativeDanmakuRect(r, s)
		position := nativePlayerPosition(s, p.Now())
		active := false
		p.Clip(video, 0, func() {
			for _, flight := range layout.flights {
				if flight.start < s.DanmakuLaunchAfter {
					continue
				}
				elapsed := position - flight.start
				if hover.id == flight.item.ID {
					elapsed = hover.at - flight.start
				}
				if elapsed < 0 || elapsed > 9 {
					continue
				}
				x := video.X + video.W - float32(elapsed*flight.speed)
				y := video.Y + float32(flight.lane)*layout.lineHeight + layout.ascent + 8
				if x+flight.width < video.X || x > video.X+video.W {
					continue
				}
				active = true
				color := ui.RGB(255, 255, 255)
				if validDanmakuColor(flight.item.Color) {
					color = ui.Hex(flight.item.Color)
				}
				if hover.id == flight.item.ID {
					color = ui.RGB(255, 216, 105)
				}
				if d.Outline {
					black := ui.RGBA(0, 0, 0, float32(d.Opacity)*.85)
					for _, offset := range [][2]float32{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
						p.Glyphs(flight.glyphs, x+offset[0], y+offset[1], black)
					}
				}
				p.Glyphs(flight.glyphs, x, y, color.Alpha(float32(d.Opacity)))
			}
		})
		if active && !s.Paused && s.Status.Playing && !s.Status.Buffering {
			p.AnimationFrameRate(60)
		}
	})
}
func validDanmakuColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
func nativeVideoRect(r ui.Rect, s playback.State) ui.Rect {
	if s.Status.Width <= 0 || s.Status.Height <= 0 || s.Preferences.Fit == "cover" {
		return r
	}
	scale := float32(math.Min(float64(r.W)/float64(s.Status.Width), float64(r.H)/float64(s.Status.Height)))
	w, h := float32(s.Status.Width)*scale, float32(s.Status.Height)*scale
	return ui.Rect{X: r.X + (r.W-w)/2, Y: r.Y + (r.H-h)/2, W: w, H: h}
}
func buildDanmakuLayout(key string, items []ipc.Danmaku, width, height float32, p playback.DanmakuPreferences) danmakuLayout {
	font := ui.Font{Size: float32(p.FontSize), Weight: p.FontWeight}
	lineHeight := float32(p.FontSize) + 10
	lanes := max(1, min(15, int((height*.75-16)/lineHeight)))
	if p.Rows > 0 {
		lanes = min(lanes, p.Rows)
	}
	last := make([]*danmakuFlight, lanes)
	ordered := append([]ipc.Danmaku(nil), items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].TimeMs == ordered[j].TimeMs {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].TimeMs < ordered[j].TimeMs
	})
	layout := danmakuLayout{key: key, width: width, height: height, lineHeight: lineHeight, ascent: font.Metrics().Ascent}
	for _, item := range ordered {
		glyphs := ui.Shape(item.Text, font)
		var textWidth float32
		for _, glyph := range glyphs {
			textWidth = max(textWidth, glyph.X+glyph.Advance)
		}
		flight := danmakuFlight{item: item, glyphs: glyphs, width: textWidth, start: float64(item.TimeMs) / 1000, speed: float64(width+textWidth) / 9, lane: -1}
		for lane, prior := range last {
			if prior == nil || danmakuLaneAvailable(*prior, flight, width) {
				flight.lane = lane
				break
			}
		}
		if flight.lane < 0 {
			continue
		}
		layout.flights = append(layout.flights, flight)
		copy := flight
		last[flight.lane] = &copy
	}
	return layout
}
func danmakuLaneAvailable(previous, next danmakuFlight, width float32) bool {
	elapsed := next.start - previous.start
	if elapsed >= 9 {
		return true
	}
	if elapsed < 0 {
		return false
	}
	previousRight := float64(width+previous.width) - elapsed*previous.speed
	gap := float64(width) - previousRight - 18
	if gap < 0 {
		return false
	}
	// A wider new item moves faster; ensure it cannot catch the previous
	// right edge before the previous item has left the clipped video area.
	if next.speed > previous.speed {
		return gap/(next.speed-previous.speed) >= 9-elapsed
	}
	return true
}

func pointInRect(x, y float32, r ui.Rect) bool {
	return r.W > 0 && r.H > 0 && x >= r.X && x <= r.X+r.W && y >= r.Y && y <= r.Y+r.H
}
func hitDanmaku(layout danmakuLayout, video ui.Rect, launchAfter, position float64, x, y float32) (danmakuFlight, ui.Rect, bool) {
	if !pointInRect(x, y, video) {
		return danmakuFlight{}, ui.Rect{}, false
	}
	for i := len(layout.flights) - 1; i >= 0; i-- {
		f := layout.flights[i]
		elapsed := position - f.start
		if f.start < launchAfter || elapsed < 0 || elapsed > 9 || (!f.item.ShowDigg && !f.item.ShowAddOne) {
			continue
		}
		r := ui.Rect{X: video.X + video.W - float32(elapsed*f.speed), Y: video.Y + float32(f.lane)*layout.lineHeight + 8, W: f.width, H: layout.lineHeight}
		if pointInRect(x, y, r) {
			return f, r, true
		}
	}
	return danmakuFlight{}, ui.Rect{}, false
}

// Danmaku belongs to the full player canvas, including black letterbox areas.
// Keep the horizontal travel at the stage edges. Controls are painted above
// danmaku; neither the rail nor a drawer may move its entry/exit boundary.
func nativeDanmakuRect(r ui.Rect, s playback.State) ui.Rect {
	top, bottom := float32(76), float32(186)
	if s.Preferences.Clear {
		top, bottom = 8, 8
	}
	r.Y += top
	r.H = max(1, r.H-top-bottom)
	return r
}
