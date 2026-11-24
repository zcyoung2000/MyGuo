package media

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/model"
	"myguo/internal/sparedevice"
	"myguo/internal/store"
)

func expirePlaybackFailure(ms *MediaServer, id string) {
	ms.pbCacheMu.Lock()
	item := ms.pbCache[id]
	item.expiresAt = time.Now().Add(-time.Second)
	ms.pbCache[id] = item
	ms.pbCacheMu.Unlock()
}

func TestFailedPlaybackLoadDeduplicatesAndCachesUntilExpiry(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	ms := &MediaServer{playbackResolver: fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil, hgapi.ErrTransport
	}}}
	var waiters sync.WaitGroup
	for range 32 {
		waiters.Go(func() {
			pb, err := ms.resolvePlayback(context.Background(), "episode")
			if pb != nil || !errors.Is(err, hgapi.ErrTransport) {
				t.Errorf("failed shared result = %v / %v", pb, err)
			}
		})
	}
	<-started
	close(release)
	waiters.Wait()
	if calls.Load() != 1 || ms.pbConsecutiveFailures != 1 {
		t.Fatalf("source calls / failures = %d / %d", calls.Load(), ms.pbConsecutiveFailures)
	}
	item := ms.pbCache["episode"]
	if ttl := time.Until(item.expiresAt); ttl <= 4*time.Second || ttl > playbackFailureTTL {
		t.Fatalf("failure TTL = %v", ttl)
	}
	_, err := ms.resolvePlayback(context.Background(), "episode")
	var delayed interface{ PlaybackRetryAfter() time.Duration }
	if !errors.As(err, &delayed) || delayed.PlaybackRetryAfter() <= 0 || calls.Load() != 1 {
		t.Fatalf("failure cache did not retain error/delay: %v, calls=%d", err, calls.Load())
	}
	expirePlaybackFailure(ms, "episode")
	_, _ = ms.resolvePlayback(context.Background(), "episode")
	if calls.Load() != 2 || ms.pbConsecutiveFailures != 2 {
		t.Fatalf("expired failure did not reload: calls=%d failures=%d", calls.Load(), ms.pbConsecutiveFailures)
	}
}

func TestPlaybackEightActualFailuresStartCooldownAndExpiryAllowsLoad(t *testing.T) {
	var calls int
	ms := &MediaServer{playbackResolver: fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
		calls++
		return nil, hgapi.ErrAnonymousLimited
	}}}
	for i := range playbackFailureThreshold {
		_, err := ms.resolvePlayback(context.Background(), fmt.Sprint(i))
		if !errors.Is(err, hgapi.ErrAnonymousLimited) {
			t.Fatal(err)
		}
	}
	if ms.pbConsecutiveFailures != 0 || time.Until(ms.pbCooldownUntil) < 14*time.Second {
		t.Fatalf("cooldown was not armed/reset: count=%d until=%v", ms.pbConsecutiveFailures, ms.pbCooldownUntil)
	}
	for range 10 {
		_, err := ms.resolvePlayback(context.Background(), "next")
		var delayed interface{ PlaybackRetryAfter() time.Duration }
		if !errors.Is(err, hgapi.ErrRateLimited) || !errors.As(err, &delayed) || delayed.PlaybackRetryAfter() < 14*time.Second {
			t.Fatalf("cooldown result = %v", err)
		}
	}
	_, err := ms.resolvePlayback(context.Background(), "0")
	var delayed interface{ PlaybackRetryAfter() time.Duration }
	if !errors.As(err, &delayed) || delayed.PlaybackRetryAfter() < 14*time.Second {
		t.Fatalf("cached failure lost global retry deadline: %v", err)
	}
	if calls != 8 || ms.pbConsecutiveFailures != 0 {
		t.Fatalf("suppression counted: calls=%d failures=%d", calls, ms.pbConsecutiveFailures)
	}
	ms.pbCooldownUntil = time.Now().Add(-time.Second)
	_, _ = ms.resolvePlayback(context.Background(), "next")
	if calls != 9 || ms.pbConsecutiveFailures != 1 {
		t.Fatalf("expired cooldown did not allow a fresh load: %d/%d", calls, ms.pbConsecutiveFailures)
	}
}

func TestPlaybackSuccessResetsConsecutiveFailures(t *testing.T) {
	ms := &MediaServer{playbackResolver: fakePlaybackResolver{func(_ context.Context, id string) (*playbackResolveResult, error) {
		if id == "good" {
			return playbackResult("good"), nil
		}
		return nil, hgapi.ErrTransport
	}}}
	for i := range 7 {
		_, _ = ms.resolvePlayback(context.Background(), fmt.Sprint(i))
	}
	if _, err := ms.resolvePlayback(context.Background(), "good"); err != nil {
		t.Fatal(err)
	}
	_, _ = ms.resolvePlayback(context.Background(), "after-success")
	if ms.pbConsecutiveFailures != 1 || !ms.pbCooldownUntil.IsZero() {
		t.Fatalf("success failed to break the streak: %d/%v", ms.pbConsecutiveFailures, ms.pbCooldownUntil)
	}
	// Replaying a cached success is not evidence that an upstream recovered.
	_, _ = ms.resolvePlayback(context.Background(), "good")
	if ms.pbConsecutiveFailures != 1 {
		t.Fatal("cache hits reset upstream failure streak")
	}
	ms.RememberPlayback("prefetched", playbackResult("prefetched").Playback)
	if ms.pbConsecutiveFailures != 0 {
		t.Fatal("successful lookahead did not reset failure streak")
	}
}

func TestPlaybackTerminalAndCanceledErrorsNeverCountOrCache(t *testing.T) {
	unknown := errors.New("unclassified failure")
	for _, cause := range []error{
		context.Canceled, context.DeadlineExceeded, hgapi.ErrAuthenticationRequired,
		hgapi.ErrDRMProtected, hgapi.ErrContentUnavailable, hgapi.ErrVideoTabEpisodeMissing,
		errPlaybackModelUnavailable, hgapi.ErrSignerUnavailable, hgapi.ErrSignatureRejected,
		hgapi.ErrChallengeRequired, hgapi.ErrParamInvalid, unknown,
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			for _, err := range []error{cause, errors.Join(hgapi.ErrTransport, cause)} {
				if cause == unknown && err != cause {
					continue // Unknown causes do not override a classified transport cause.
				}
				calls := 0
				ms := &MediaServer{pbConsecutiveFailures: 3, playbackResolver: fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
					calls++
					return nil, err
				}}}
				for range 2 {
					_, got := ms.resolvePlayback(context.Background(), "episode")
					if !errors.Is(got, cause) {
						t.Fatalf("cause lost: %v", got)
					}
				}
				if calls != 2 || ms.pbConsecutiveFailures != 3 || len(ms.pbCache) != 0 {
					t.Fatalf("terminal error counted/cached: %d/%d/%v", calls, ms.pbConsecutiveFailures, ms.pbCache)
				}
			}
		})
	}
}

func TestCanceledPlaybackOwnerCannotPublishResolverResult(t *testing.T) {
	for _, resultErr := range []error{nil, hgapi.ErrTransport} {
		ctx, cancel := context.WithCancel(context.Background())
		ms := &MediaServer{playbackResolver: fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
			cancel()
			return playbackResult("late"), resultErr
		}}}
		if pb, err := ms.resolvePlayback(ctx, "episode"); pb != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled owner published %v/%v", pb, err)
		}
		if len(ms.pbCache) != 0 || ms.pbConsecutiveFailures != 0 {
			t.Fatal("cancellation changed cache/streak")
		}
	}
}

func TestSpareAuthenticationFailureDoesNotCountPrimarySourceFailure(t *testing.T) {
	state, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.UpsertSpareDevice(store.SpareDevice{DeviceID: "did", InstallID: "iid"}); err != nil {
		t.Fatal(err)
	}
	pool := sparedevice.NewManager(state, sparedevice.WithClientFactory(func(hgapi.ProfileStore) (sparedevice.PlaybackResolver, error) {
		return func(context.Context, string, hgapi.PlaybackOptions) (*model.Playback, error) {
			return nil, hgapi.ErrAuthenticationRequired
		}, nil
	}))
	ms := &MediaServer{playbackResolver: fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
		return nil, hgapi.ErrAnonymousLimited
	}}}
	ms.SetSpareDevicePool(pool)
	_, err = ms.resolvePlayback(context.Background(), "episode")
	if !errors.Is(err, hgapi.ErrAuthenticationRequired) || ms.pbConsecutiveFailures != 0 || len(ms.pbCache) != 0 {
		t.Fatalf("spare auth cause was lost/counted: err=%v failures=%d cache=%v", err, ms.pbConsecutiveFailures, ms.pbCache)
	}
}

type retryingPlaybackResolver struct {
	PlaybackResolver
	retries []string
}

func (r *retryingPlaybackResolver) RetryPlaybackNow(id string) { r.retries = append(r.retries, id) }

func TestManualPlaybackRetryInvalidatesEpisodeAndResetsCooldownOnce(t *testing.T) {
	var calls int
	r := &retryingPlaybackResolver{PlaybackResolver: fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
		calls++
		return nil, hgapi.ErrTransport
	}}}
	ms := &MediaServer{playbackResolver: newPlaybackRaceResolver(&playbackFallbackResolver{inner: r}, nil)}
	_, _ = ms.resolvePlayback(context.Background(), "episode")
	other := playbackResult("other").Playback
	ms.RememberPlayback("other", other)
	ms.pbCooldownUntil = time.Now().Add(time.Minute)
	ms.pbConsecutiveFailures = 7
	ctx, cancel := context.WithTimeout(WithPlaybackRetry(context.Background()), time.Second)
	defer cancel()
	_, _ = ms.resolvePlayback(ctx, "episode")
	_, _ = ms.resolvePlayback(ctx, "episode")
	if calls != 2 || ms.pbConsecutiveFailures != 1 || !ms.pbCooldownUntil.IsZero() {
		t.Fatalf("manual retry did not clear exactly once: calls=%d count=%d cooldown=%v", calls, ms.pbConsecutiveFailures, ms.pbCooldownUntil)
	}
	if len(r.retries) != 1 || r.retries[0] != "episode" || ms.pbCache["other"].pb != other {
		t.Fatalf("retry was not scoped/forwarded: %v", r.retries)
	}
	ms.RememberPlayback("episode", playbackResult("stale-url").Playback)
	ms.RetryPlaybackNow("episode")
	if _, cached := ms.pbCache["episode"]; cached {
		t.Fatal("manual retry retained an old successful source")
	}
}

func TestManualRetryDetachedFlightCannotOverwriteFreshFlight(t *testing.T) {
	for _, oldErr := range []error{nil, hgapi.ErrTransport} {
		t.Run(fmt.Sprint(oldErr), func(t *testing.T) {
			oldStarted, newStarted := make(chan struct{}), make(chan struct{})
			oldRelease, newRelease := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			ms := &MediaServer{playbackResolver: fakePlaybackResolver{func(context.Context, string) (*playbackResolveResult, error) {
				if calls.Add(1) == 1 {
					close(oldStarted)
					<-oldRelease
					return playbackResult("stale"), oldErr
				}
				close(newStarted)
				<-newRelease
				return playbackResult("fresh"), nil
			}}}
			oldDone, newDone := make(chan struct{}), make(chan struct{})
			go func() { _, _ = ms.resolvePlayback(context.Background(), "episode"); close(oldDone) }()
			<-oldStarted
			ms.RetryPlaybackNow("episode")
			go func() { _, _ = ms.resolvePlayback(context.Background(), "episode"); close(newDone) }()
			<-newStarted
			close(oldRelease)
			<-oldDone
			ms.pbLoadMu.Lock()
			active := ms.pbLoads["episode"] != nil
			ms.pbLoadMu.Unlock()
			if !active || len(ms.pbCache) != 0 || ms.pbConsecutiveFailures != 0 {
				t.Error("stale completion published or deleted the newer flight")
			}
			close(newRelease)
			<-newDone
			pb, err := ms.resolvePlayback(context.Background(), "episode")
			if err != nil || pb.VideoID != "fresh" || calls.Load() != 2 {
				t.Fatalf("fresh cache = %v/%v calls=%d", pb, err, calls.Load())
			}
		})
	}
}

// Compile-time guard for the optional controller error contract.
var _ interface{ PlaybackRetryAfter() time.Duration } = (*playbackRetryError)(nil)
