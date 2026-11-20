package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestReleaseCalendarKeepsDatesInOneScrollableRow(t *testing.T) {
	app := NewTestApp()
	app.view = "new"
	app.releases = ReleaseState{
		Calendar: true,
		Date:     "20261120",
		Dates: []string{
			"20261113", "20261114", "20261115", "20261116", "20261117",
			"20261118", "20261119", "20261120", "20261121", "20261122",
			"20261123", "20261124", "20261125", "20261126", "20261127",
		},
	}
	tester := ui.NewTester(app.Render, 1200, 800)

	viewport, ok := tester.Find("新剧日期")
	if !ok {
		t.Fatal("calendar date scroller is missing")
	}
	first, firstOK := tester.Find("11/13")
	last, lastOK := tester.Find("11/27")
	if !firstOK || !lastOK {
		t.Fatalf("date range is missing: first=%v last=%v", firstOK, lastOK)
	}
	if first.Y != last.Y {
		t.Fatalf("calendar dates wrapped across rows: first y=%.1f last y=%.1f", first.Y, last.Y)
	}

	tester.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 900, 0)
	tester.Advance(500 * time.Millisecond)
	last, ok = tester.Find("11/27")
	if !ok || last.X >= viewport.X+viewport.W || last.X+last.W <= viewport.X {
		t.Fatalf("last calendar date did not scroll into view: viewport=%+v last=%+v found=%v", viewport, last, ok)
	}
}

func TestReleaseCalendarKeepsSelectedDayHighlighted(t *testing.T) {
	app := NewTestApp()
	app.view = "new"
	app.releases = ReleaseState{Calendar: true, Date: "20261120", Dates: []string{"20261120", "20261121"}}
	tt := ui.NewTesterWith(app.Render, 1200, 800, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	for _, label := range []string{"2026-11-20", "2026-11-21"} {
		bounds, ok := tt.Find(label)
		if !ok {
			t.Fatalf("missing date button %s", label)
		}
		tt.Move(bounds.X+bounds.W/2, bounds.Y+bounds.H/2)
		tt.Advance(250 * time.Millisecond)
		selected, _ := tt.Find("2026-11-20")
		pixel := tt.Image().RGBAAt(int(selected.X+selected.W/2), int(selected.Y+selected.H-5))
		if pixel.R < 100 || int(pixel.R) < 2*int(pixel.G) {
			t.Fatalf("hovering %s covered the selected day highlight: %+v", label, pixel)
		}
	}
	exportAuditScreenshot(t, tt, "calendar-selected-day")
}
