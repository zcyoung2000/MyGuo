package uiapp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
	"myguo/internal/playback"
)

func TestNativePlayerPanelContentClearsScrollbarHitArea(t *testing.T) {
	for _, trackWidth := range []float32{0, 20} {
		for _, panel := range []string{"settings", "episodes", "comments", "danmaku-items"} {
			t.Run(fmt.Sprintf("track-%g/%s", trackWidth, panel), func(t *testing.T) {
				p := newNativeUIPlayer()
				p.state.Mode = playback.Series
				p.state.Panel = panel
				// Long text reaches the available width; enough rows force overflow.
				text := strings.Repeat("右侧内容应避开滚动条", 4)
				for i := 0; i < 40; i++ {
					p.state.CommentItems = append(p.state.CommentItems, ipc.CommentDTO{
						ID: fmt.Sprint(i), Author: "剧友", Content: fmt.Sprintf("评论%d%s", i, text),
					})
					p.state.DanmakuItems = append(p.state.DanmakuItems, ipc.Danmaku{
						ID: fmt.Sprint(i), Text: fmt.Sprintf("弹幕%d%s", i, text), TimeMs: int64(i * 1000),
					})
				}
				app := NewTestApp()
				var style ui.ScrollbarStyle
				tt := ui.NewTesterWith(func(c *ui.Context) {
					theme := *c.Theme()
					theme.ScrollbarStyle.TrackWidth = trackWidth
					c.SetTheme(&theme)
					style = (ui.ScrollbarStyle{}).Resolve(c.Theme())
					ui.Box(c).Fill().Children(func() {
						app.renderNativePlayerPanel(c, p.state, 320, p.Command)
					})
				}, 400, 640, ui.TesterOptions{
					Preferences: ui.Preferences{ReduceMotion: true, TextScale: 1},
				})
				tt.Frame()
				role := ui.RoleList
				if panel == "settings" {
					role = ui.RoleScroll
				}
				scroll, ok := tt.AccessByRole(role, "")
				if !ok {
					t.Fatal("missing panel scroller")
				}
				label := map[string]string{
					"settings": "1080p", "episodes": "12",
					"comments": p.state.CommentItems[1].Content, "danmaku-items": p.state.DanmakuItems[1].Text,
				}[panel]
				bounds := func() ui.Rect {
					t.Helper()
					if panel == "settings" || panel == "episodes" {
						item, found := tt.AccessByRole(ui.RoleButton, label)
						if found {
							return item.Bounds
						}
					} else if item, found := tt.Find(label); found {
						return item
					}
					t.Fatalf("missing panel content %q", label)
					return ui.Rect{}
				}
				before := bounds()
				trackLeft := scroll.Bounds.X + scroll.Bounds.W - style.TrackWidth
				if before.W <= 0 || before.H <= 0 || before.X+before.W > trackLeft-1 {
					t.Fatalf("content overlaps scrollbar hit area: content=%+v trackLeft=%g", before, trackLeft)
				}
				// Click the right edge of the fourth episode column / quality button,
				// where an overlay scrollbar used to intercept the action.
				if panel == "settings" || panel == "episodes" {
					tt.Move(before.X+before.W-2, before.Y+before.H/2)
					tt.ClickAt(before.X+before.W-2, before.Y+before.H/2)
					command := "quality"
					if panel == "episodes" {
						command = "episode"
					}
					if len(p.commands) != 1 || p.commands[0] != command {
						t.Fatalf("right-edge click lost: commands=%v", p.commands)
					}
				}
				tt.Scroll(scroll.Bounds.X+10, scroll.Bounds.Y+10, 0, 36)
				after := bounds()
				if after.Y >= before.Y {
					t.Fatalf("panel did not scroll: before=%+v after=%+v", before, after)
				}
				if after.X+after.W > trackLeft-1 {
					t.Fatalf("scrolled content overlaps scrollbar: content=%+v trackLeft=%g", after, trackLeft)
				}
				// The reserved strip must still drag the scrollbar without firing
				// an episode or setting action underneath it.
				tt.Scroll(scroll.Bounds.X+10, scroll.Bounds.Y+10, 0, -36)
				commands := len(p.commands)
				x, y := trackLeft+style.TrackWidth/2, scroll.Bounds.Y+10
				tt.Move(x, y)
				tt.Press(x, y)
				tt.Move(x, y+60)
				tt.Release(x, y+60)
				if item, found := tt.Find(label); found && item.H > 0 && item.Y >= before.Y {
					t.Fatalf("scrollbar drag did not move content: before=%+v after=%+v", before, item)
				}
				if len(p.commands) != commands {
					t.Fatalf("scrollbar drag activated content: commands=%v", p.commands)
				}
			})
		}
	}
}
