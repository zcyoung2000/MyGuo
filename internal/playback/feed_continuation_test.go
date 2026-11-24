package playback

import (
	"testing"
	"time"

	"myguo/internal/ipc"
)

func TestFeedEpisodeEndRequestsIndependentSeriesSession(t *testing.T) {
	b := &testBackend{restore: &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "A", EpisodeCount: 3}}}}
	r := &engineRegistry{}
	type continuation struct {
		scene  uint64
		id     string
		number int
	}
	requests := make(chan continuation, 8)
	c := New(b, r.make, "", Hooks{ContinueFeed: func(scene uint64, id string, number int) {
		requests <- continuation{scene, id, number}
	}})
	t.Cleanup(c.Close)
	c.ShowFeed("漫剧")
	initial := waitSource(t, c, "A", 1)
	e := c.Snapshot().Engine.(*testEngine)
	e.mu.Lock()
	e.status.Position = e.status.Duration
	e.status.Playing = false
	e.status.Ended = true
	e.mu.Unlock()
	var request continuation
	select {
	case request = <-requests:
	case <-time.After(time.Second):
		t.Fatal("ended feed did not request a series session")
	}
	if request.scene != initial.Scene || request.id != "A" || request.number != 2 {
		t.Fatalf("wrong continuation: %+v", request)
	}
	// Repeated native EOF snapshots must not launch another session while the
	// navigation owner is processing the callback.
	c.post(func() { c.tick(time.Now()); c.tick(time.Now()) })
	barrier(t, c)
	select {
	case duplicate := <-requests:
		t.Fatalf("duplicate continuation: %+v", duplicate)
	default:
	}
	if s := c.Snapshot(); s.Mode != Feed || s.Playback.EpisodeNum != 1 {
		t.Fatal("controller bypassed the navigation owner")
	}
	c.Play(request.id, request.number, -1, 1)
	continued := waitSource(t, c, "A", 2)
	if continued.Mode != Series || continued.Session != 1 || continued.Paused {
		t.Fatalf("continuation did not enter series playback: %+v", continued)
	}
}

func TestDisplayedFrameSurvivesSeekButResetsForNewSelection(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.Play("A", 1, 0, 1)
	waitSource(t, c, "A", 1)
	eventually(t, func() bool { return c.Snapshot().HasDisplayedFrame })
	e := c.Snapshot().Engine.(*testEngine)
	c.Command("seek", 70.0)
	barrier(t, c)
	e.mu.Lock()
	e.status.Ready, e.status.Displayed, e.status.Buffering = false, false, true
	e.mu.Unlock()
	eventually(t, func() bool { return !c.Snapshot().Status.Ready })
	if !c.Snapshot().HasDisplayedFrame {
		t.Fatal("seek cleared the displayed picture latch")
	}
	reset := make(chan bool, 1)
	c.post(func() {
		c.selectSlot(ipc.SeriesCard{ID: "B"}, 1, 0, 0)
		reset <- c.state.HasDisplayedFrame
	})
	if <-reset {
		t.Fatal("new episode inherited the previous picture latch")
	}
}
