package playerengine

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	"myguo/internal/avkit"
)

type fakeSystemVideo struct {
	check        func()
	s            avkit.Status
	rate, volume float64
	muted        bool
	closes       int
}

func (v *fakeSystemVideo) play()          { v.check(); v.s.Playing = true }
func (v *fakeSystemVideo) pause()         { v.check(); v.s.Playing = false }
func (v *fakeSystemVideo) seek(n float64) { v.check(); v.s.Position = n }
func (v *fakeSystemVideo) configure(r, vol float64, m bool) {
	v.check()
	v.rate, v.volume, v.muted = r, vol, m
}
func (v *fakeSystemVideo) snapshot() avkit.Status  { v.check(); return v.s }
func (v *fakeSystemVideo) player() *ui.VideoPlayer { v.check(); return nil }
func (v *fakeSystemVideo) close()                  { v.check(); v.closes++ }

func TestSystemPlayerPreservesIntentAcrossLoadAndClearsFailedSources(t *testing.T) {
	onMain := false
	dispatch := func(fn func()) { onMain = true; defer func() { onMain = false }(); fn() }
	check := func() {
		if !onMain {
			t.Fatal("video call escaped the UI dispatcher")
		}
	}
	var videos []*fakeSystemVideo
	p := newSystemPlayer(func(url string) (systemVideo, error) {
		check()
		if url == "failed" {
			return nil, errors.New("source failure")
		}
		v := &fakeSystemVideo{check: check, s: avkit.Status{Ready: true, Displayed: true, Duration: 60}}
		videos = append(videos, v)
		return v, nil
	}, dispatch)
	p.Configure(1.5, .3, true, "cover")
	if err := p.Load("first"); err != nil {
		t.Fatal(err)
	}
	if p.Snapshot().Playing || videos[0].rate != 1.5 || videos[0].volume != .3 || !videos[0].muted {
		t.Fatal("loading lost configuration or started playing before controller intent")
	}
	p.Play()
	p.Seek(12)
	if s := p.Snapshot(); !s.Playing || s.Position != 12 || !s.Displayed {
		t.Fatalf("status = %+v", s)
	}
	p.Seek(math.NaN())
	p.Seek(math.Inf(1))
	if p.Snapshot().Position != 12 {
		t.Fatal("invalid seek changed the clock")
	}
	p.Preroll()
	if p.Snapshot().Playing {
		t.Fatal("background preroll played audio")
	}
	if err := p.Load("second"); err != nil {
		t.Fatal(err)
	}
	if videos[0].closes != 1 || p.Snapshot().Position != 0 || p.Snapshot().Playing {
		t.Fatal("reload retained stale media")
	}
	if err := p.Load("failed"); err == nil {
		t.Fatal("source error was swallowed")
	}
	if s := p.Snapshot(); s.Ready || s.Displayed || s.Position != 0 {
		t.Fatalf("failed load retained old frame/clock: %+v", s)
	}
	if videos[1].closes != 1 {
		t.Fatal("failed replacement leaked old player")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	p.Close()
	p.Play()
	if err := p.Load("third"); err == nil {
		t.Fatal("closed player reopened")
	}
}

func TestSystemPlayerSerializesConcurrentControllerAndRendererCalls(t *testing.T) {
	type work struct {
		fn   func()
		done chan struct{}
	}
	queue := make(chan work)
	onMain := false
	go func() {
		for w := range queue {
			onMain = true
			w.fn()
			onMain = false
			close(w.done)
		}
	}()
	defer close(queue)
	dispatch := func(fn func()) { done := make(chan struct{}); queue <- work{fn, done}; <-done }
	v := &fakeSystemVideo{check: func() {
		if !onMain {
			t.Error("unmarshaled video access")
		}
	}}
	p := newSystemPlayer(func(string) (systemVideo, error) { v.check(); return v, nil }, dispatch)
	if err := p.Load("test"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 20; j++ {
				p.Configure(1, .5, false, "contain")
				p.Play()
				p.Seek(float64(j))
				p.Snapshot()
				p.VideoPlayer()
				p.Pause()
			}
		})
	}
	wg.Wait()
	p.Close()
	if v.closes != 1 {
		t.Fatal("player was not released exactly once")
	}
}

func TestSystemPlayerPiPRequiresFrameAndHonorsCancellation(t *testing.T) {
	p := newSystemPlayer(func(string) (systemVideo, error) {
		return &fakeSystemVideo{check: func() {}}, nil
	}, func(fn func()) { fn() })
	if err := p.TogglePictureInPicture(context.Background()); err == nil {
		t.Fatal("empty player entered PiP")
	}
	if err := p.Load("test"); err != nil {
		t.Fatal(err)
	}
	if err := p.TogglePictureInPicture(context.Background()); err == nil {
		t.Fatal("loading player entered PiP")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.TogglePictureInPicture(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("PiP cancellation = %v", err)
	}
}

func TestSystemPlayerDefersStartupSeekUntilAFrameIsReady(t *testing.T) {
	v := &fakeSystemVideo{check: func() {}}
	p := newSystemPlayer(func(string) (systemVideo, error) { return v, nil }, func(fn func()) { fn() })
	if err := p.Load("first"); err != nil {
		t.Fatal(err)
	}
	p.Seek(25)
	p.Play()
	if p.Snapshot().Position != 0 {
		t.Fatal("seek before readiness was sent to the media engine")
	}
	v.s.Ready, v.s.Displayed = true, true
	if s := p.Snapshot(); s.Position != 25 || !s.Playing {
		t.Fatalf("pending startup seek was not applied: %+v", s)
	}
	v.s.Ready = false
	p.Seek(40)
	if err := p.Load("replacement"); err != nil {
		t.Fatal(err)
	}
	v.s.Position, v.s.Ready = 0, true
	if p.Snapshot().Position != 0 {
		t.Fatal("source replacement inherited a pending seek")
	}
}

func TestSystemPiPButtonsReportUserIntentWithoutWaitingForAPoll(t *testing.T) {
	v := &fakeSystemVideo{check: func() {}, s: avkit.Status{Ready: true}}
	p := newSystemPlayer(func(string) (systemVideo, error) { return v, nil }, func(fn func()) { fn() })
	var intents []bool
	p.SetPlaybackIntentHandler(func(paused bool) { intents = append(intents, paused) })
	if err := p.Load("test"); err != nil {
		t.Fatal(err)
	}
	p.Play()
	if len(intents) != 0 {
		t.Fatal("controller play was reported as a user action")
	}
	p.togglePiPPlayback()
	if v.s.Playing || len(intents) != 1 || !intents[0] {
		t.Fatal("PiP pause did not immediately report intent")
	}
	p.togglePiPPlayback()
	if !v.s.Playing || len(intents) != 2 || intents[1] {
		t.Fatal("PiP resume did not immediately report intent")
	}
	p.Close()
	p.togglePiPPlayback()
	if len(intents) != 2 {
		t.Fatal("closed PiP reported another user action")
	}
}
