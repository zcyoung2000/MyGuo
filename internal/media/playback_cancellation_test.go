package media

import (
	"context"
	"errors"
	"testing"
	"time"

	"myguo/internal/hgapi"
)

func TestPlaybackRaceTimeoutDoesNotWaitForUnresponsiveSource(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	r := newPlaybackRaceResolver(fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
		<-release
		return nil, hgapi.ErrAnonymousLimited
	}}, nil)
	r.timeout = 20 * time.Millisecond
	done := make(chan error, 1)
	go func() {
		_, err := r.ResolvePlayback(context.Background(), "71001")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout cause was lost: %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("playback waited past its deadline for an unresponsive source")
	}
}

func TestPlaybackTerminalErrorDoesNotWaitForUnresponsiveSibling(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	r := newPlaybackRaceResolver(fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
		return nil, hgapi.ErrParamInvalid
	}}, fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
		<-release
		return nil, hgapi.ErrAnonymousLimited
	}})
	done := make(chan error, 1)
	go func() {
		_, err := r.ResolvePlayback(context.Background(), "71001")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, hgapi.ErrParamInvalid) {
			t.Fatalf("terminal cause was lost: %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("terminal error waited for a source that ignored cancellation")
	}
}
