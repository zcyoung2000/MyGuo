package media

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"myguo/internal/hgapi/model"
)

type playbackWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *playbackWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestActivePlaybackWaiterRecoversFromCanceledOwner(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			load := &playbackLoad{done: make(chan struct{})}
			want := playbackResult("fresh").Playback
			ms := &MediaServer{
				pbLoads: map[string]*playbackLoad{"episode": load},
				playbackResolver: fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
					return &playbackResolveResult{Playback: want}, nil
				}},
			}
			base, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			ctx := &playbackWaitContext{Context: base, waiting: make(chan struct{})}
			result := make(chan playbackLoadResult, 1)
			go func() {
				pb, err := ms.resolvePlayback(ctx, "episode")
				result <- playbackLoadResult{pb: pb, err: err}
			}()
			select {
			case <-ctx.waiting:
			case <-base.Done():
				t.Fatal("foreground did not join the background model load")
			}
			ms.finishPlaybackLoad("episode", load, nil, cause)
			select {
			case got := <-result:
				if got.pb != want || got.err != nil {
					t.Fatalf("active foreground inherited background cancellation: %v/%v", got.pb, got.err)
				}
			case <-base.Done():
				t.Fatal("foreground did not recover after its background owner stopped")
			}
		})
	}
}

func TestPlaybackCanReplaceCanceledOwnerBeforeCleanup(t *testing.T) {
	owner, stopOwner := context.WithCancel(context.Background())
	stopOwner()
	old := &playbackLoad{done: make(chan struct{}), owner: owner}
	want := playbackResult("fresh").Playback
	ms := &MediaServer{
		pbLoads: map[string]*playbackLoad{"episode": old},
		playbackResolver: fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
			return &playbackResolveResult{Playback: want}, nil
		}},
	}
	defer ms.finishPlaybackLoad("episode", old, nil, context.Canceled)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if got, err := ms.resolvePlayback(ctx, "episode"); got != want || err != nil {
		t.Fatalf("foreground waited for a cancelled owner's cleanup: %v/%v", got, err)
	}
}

func TestPlaybackLoadBroadcastsResultToEveryWaiter(t *testing.T) {
	for _, resolveErr := range []error{nil, errors.New("source unavailable")} {
		load := &playbackLoad{done: make(chan struct{})}
		ms := &MediaServer{pbLoads: map[string]*playbackLoad{"episode": load}}
		want := &model.Playback{Streams: []model.Stream{{URL: "https://media.invalid/video"}}}
		var waiters sync.WaitGroup
		for i := 0; i < 32; i++ {
			waiters.Add(1)
			go func() {
				defer waiters.Done()
				pb, err := ms.resolvePlayback(context.Background(), "episode")
				if pb != want || err != resolveErr {
					t.Errorf("shared resolve = %p/%v, want %p/%v", pb, err, want, resolveErr)
				}
			}()
		}
		load.result = playbackLoadResult{pb: want, err: resolveErr}
		close(load.done)
		waiters.Wait()
	}
}

func TestPlaybackLoadWaiterCanCancelIndependently(t *testing.T) {
	load := &playbackLoad{done: make(chan struct{})}
	ms := &MediaServer{pbLoads: map[string]*playbackLoad{"episode": load}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ms.resolvePlayback(ctx, "episode"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter = %v", err)
	}
	// Canceling this waiter must not consume or cancel the shared result.
	load.result.pb = &model.Playback{}
	close(load.done)
	if pb, err := ms.resolvePlayback(context.Background(), "episode"); pb != load.result.pb || err != nil {
		t.Fatalf("next waiter = %p/%v", pb, err)
	}
}
