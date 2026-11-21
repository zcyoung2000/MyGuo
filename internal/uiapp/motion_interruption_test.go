package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestHoverFeedbackUsesAnimateColorAndSnappyPress(t *testing.T) {
	if feedbackAnimationValues.Duration != 120*time.Millisecond {
		t.Fatal("hover retargeting must use the 120ms feedback token")
	}
	if cardSpring.Spring.Bounce != ui.Snappy.Spring.Bounce || cardSpring.Spring.Duration != ui.Snappy.Spring.Duration {
		t.Fatal("press feedback must use the MyGo Snappy spring")
	}
	idle, hovered := ui.Transparent, ui.RGBA(255, 255, 255, 0.08)
	element := hoverBackground(ui.Box(nil), idle, hovered)
	if element.Hovered() {
		t.Fatal("a new hover helper must not synthesize hover state")
	}
}

func TestInteractionTransitionsSettleWithoutContinuousFrames(t *testing.T) {
	app := NewTestApp()
	app.search.Items = []CatalogItem{{ID: "123456", EpisodeID: "654321", Title: "反馈稳定样本"}}
	tester := ui.NewTester(app.Render, 1000, 800)
	tester.Move(320, 240)
	tester.Frame()
	tester.Advance(300 * time.Millisecond)
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("hover and press feedback requested continuous rebuild frames")
	}
}

func TestArtworkRevealStaysPaintOnly(t *testing.T) {
	if artworkFade.Duration != 320*time.Millisecond || !artworkFade.NonMotion {
		t.Fatal("artwork reveal must remain a 320ms non-motion fade")
	}
}

func TestPlayerChromeTransitionFadesInAndOut(t *testing.T) {
	visible := false
	tt := ui.NewTester(func(c *ui.Context) {
		c.Root().Background(ui.RGB(0, 0, 0))
		if visible {
			ui.Box(c.Key("player-chrome-motion-fixture")).Absolute().Left(20).Top(20).Size(40, 40).
				Background(ui.RGB(255, 0, 0)).Transition(playerChromeTransition)
		}
	}, 100, 100)
	tt.Frame()
	visible = true
	tt.Frame()
	start := chromeTransitionRed(t, tt)
	tt.Advance(playerChromeTransition.Duration / 2)
	midIn := chromeTransitionRed(t, tt)
	tt.Advance(playerChromeTransition.Duration)
	endIn := chromeTransitionRed(t, tt)
	if !(start < midIn && midIn < endIn) {
		t.Fatalf("chrome did not fade in progressively: %d, %d, %d", start, midIn, endIn)
	}
	visible = false
	tt.Frame()
	start = chromeTransitionRed(t, tt)
	tt.Advance(playerChromeTransition.Duration / 2)
	midOut := chromeTransitionRed(t, tt)
	tt.Advance(playerChromeTransition.Duration)
	endOut := chromeTransitionRed(t, tt)
	if !(start > midOut && midOut > endOut) {
		t.Fatalf("chrome did not fade out progressively: %d, %d, %d", start, midOut, endOut)
	}
	if tt.FramePending() {
		t.Fatal("player chrome transition kept requesting frames after it settled")
	}
}

func chromeTransitionRed(t *testing.T, tt *ui.Tester) uint32 {
	t.Helper()
	r, _, _, _ := tt.Image().At(30, 30).RGBA()
	return r
}
