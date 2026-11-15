package uiapp

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Motion tokens are shared by native pages, loading states and controls.
// MyGo retargets transitions and honors Reduce Motion; artwork only repaints.
var (
	playerChromeTransition   = ui.ElementTransition{Duration: 140 * time.Millisecond, Colors: true, Enter: &ui.Motion{Opacity: 0}, Exit: &ui.Motion{Opacity: 0}}
	selectionAnimation       = ui.Animation{Duration: selectionTransition.Duration, Ease: selectionTransition.Ease}
	feedbackTransition       = ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true}
	selectionTransition      = ui.ElementTransition{Duration: 160 * time.Millisecond, Ease: ui.CubicBezier(.23, 1, .32, 1), Colors: true}
	layoutTransition         = ui.ElementTransition{Duration: 180 * time.Millisecond, Ease: ui.CubicBezier(.23, 1, .32, 1), Position: true, Size: true, Colors: true}
	hostedPageTransition     = ui.ElementTransition{Duration: 160 * time.Millisecond, Ease: ui.CubicBezier(.23, 1, .32, 1), Colors: true, Enter: &ui.Motion{Opacity: 0}}
	pageTransition           = ui.ElementTransition{Duration: 160 * time.Millisecond, Ease: ui.CubicBezier(.23, 1, .32, 1), Colors: true, Enter: &ui.Motion{Opacity: 0, Y: 6}, Exit: &ui.Motion{Opacity: 0}}
	resultTransition         = ui.ElementTransition{Duration: 180 * time.Millisecond, Ease: ui.CubicBezier(.23, 1, .32, 1), Colors: true, Enter: &ui.Motion{Opacity: 0}, Exit: &ui.Motion{Opacity: 0}}
	sectionTransition        = ui.ElementTransition{Duration: 200 * time.Millisecond, Ease: ui.CubicBezier(.23, 1, .32, 1), Height: true}
	resultPositionTransition = ui.ElementTransition{Duration: 200 * time.Millisecond, Ease: ui.CubicBezier(.23, 1, .32, 1), Position: true}
	textChange               = ui.ContentTransition{Animation: &feedbackAnimationValues}
	artworkFade              = ui.Animation{Duration: 320 * time.Millisecond, NonMotion: true}
	cardSpring               = ui.Snappy
	feedbackAnimationValues  = ui.Animation{Duration: 120 * time.Millisecond}
	drawerTransition         = ui.ElementTransition{Duration: 240 * time.Millisecond, Ease: ui.CubicBezier(.32, .72, 0, 1), Position: true, Colors: true}
)

func inlineError(c *ui.Context, label string, retry func()) {
	ui.Row(c.Key("refresh-error")).Gap(10).Padding(6, 0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted).Grow(1)
		panelAction(c, "refresh-retry", "重试", retry)
	})
}
