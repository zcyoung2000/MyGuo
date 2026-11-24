package media_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"myguo/internal/avkit"
	"myguo/internal/hgapi"
	"myguo/internal/hgapi/model"
	"myguo/internal/ipc"
	"myguo/internal/media"
	"myguo/internal/playback"
)

type retryControllerBackend struct {
	playback.Backend
	mu           sync.Mutex
	requestTimes []time.Time
	resolve      func(context.Context, int) error
}

func (b *retryControllerBackend) PlaybackScope() string                            { return "test" }
func (b *retryControllerBackend) FlushPendingProgress()                            {}
func (b *retryControllerBackend) IsFavorite(context.Context, string) (bool, error) { return false, nil }
func (b *retryControllerBackend) GetSeriesDetail(context.Context, string) (*ipc.SeriesDetailDTO, error) {
	return &ipc.SeriesDetailDTO{ID: "series", Episodes: []ipc.EpisodeDTO{{ID: "episode", Number: 1, Playable: true}}}, nil
}
func (b *retryControllerBackend) UpdateFeedPreloadWindow(context.Context, int64, string, string, []media.FeedPreloadItem) ([]media.FeedPreloadStatus, error) {
	return nil, nil
}
func (b *retryControllerBackend) PlayEpisode(ctx context.Context, _, _ string, _ int, _ string, _ bool) (*ipc.PlaybackDTO, error) {
	b.mu.Lock()
	b.requestTimes = append(b.requestTimes, time.Now())
	n := len(b.requestTimes)
	b.mu.Unlock()
	return nil, b.resolve(ctx, n)
}

type retryControllerHookBackend struct {
	*retryControllerBackend
	retried chan string
}

func (b *retryControllerHookBackend) RetryPlaybackNow(id string) { b.retried <- id }

func newRetryController(t *testing.T, b playback.Backend) *playback.Controller {
	t.Helper()
	c := playback.New(b, func() (avkit.Player, error) {
		t.Error("failed source allocated a decoder")
		return nil, errors.New("no decoder")
	}, "", playback.Hooks{})
	t.Cleanup(c.Close)
	return c
}

func waitRetryController(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("controller condition timed out")
}

func TestControllerManualRetryReachesMediaWithoutMandatoryBackendHook(t *testing.T) {
	for _, withHook := range []bool{false, true} {
		t.Run(map[bool]string{false: "context-fallback", true: "optional-hook"}[withHook], func(t *testing.T) {
			ms := &media.MediaServer{}
			ms.RememberPlayback("episode", &model.Playback{Streams: []model.Stream{{URL: "https://media.invalid/stale", Definition: "720p"}}})
			checked := make(chan error, 1)
			base := &retryControllerBackend{resolve: func(ctx context.Context, n int) error {
				if n > 1 {
					// The media cache initially contains a valid source. Manual retry
					// must evict it before resolution, even through a derived timeout.
					ctx, cancel := context.WithTimeout(ctx, time.Second)
					defer cancel()
					_, err := ms.PlaybackQualities(ctx, "episode", "720p")
					checked <- err
				}
				return hgapi.ErrAuthenticationRequired // no automatic retry in this test
			}}
			var backend playback.Backend = base
			hook := &retryControllerHookBackend{retryControllerBackend: base, retried: make(chan string, 1)}
			if withHook {
				backend = hook
			}
			c := newRetryController(t, backend)
			c.Play("series", 1, 0, 1)
			waitRetryController(t, func() bool { s := c.Snapshot(); return s.Error != "" && len(s.Episodes) > 0 })
			c.Command("retry", nil)
			select {
			case err := <-checked:
				if err == nil {
					t.Fatal("manual retry reused a cached playback source")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("manual retry did not resolve")
			}
			if withHook {
				select {
				case id := <-hook.retried:
					if id != "episode" {
						t.Fatalf("retry hook ID = %q", id)
					}
				default:
					t.Fatal("optional backend hook was not invoked before resolution")
				}
			}
		})
	}
}

type controllerRetryDelay struct{ until time.Time }

func (e controllerRetryDelay) Error() string                     { return "recoverable source failure" }
func (e controllerRetryDelay) PlaybackRetryAfter() time.Duration { return max(0, time.Until(e.until)) }

func TestSelectedSlotAutomaticRetryHonorsBackendCacheDelay(t *testing.T) {
	second := make(chan struct{}, 1)
	b := &retryControllerBackend{resolve: func(_ context.Context, n int) error {
		if n == 1 {
			return errors.Join(hgapi.ErrTransport, controllerRetryDelay{until: time.Now().Add(1500 * time.Millisecond)})
		}
		second <- struct{}{}
		return hgapi.ErrAuthenticationRequired
	}}
	c := newRetryController(t, b)
	c.Play("series", 1, 0, 1)
	select {
	case <-second:
	case <-time.After(3 * time.Second):
		t.Fatal("selected source did not automatically retry")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.requestTimes) != 2 || b.requestTimes[1].Sub(b.requestTimes[0]) < 1500*time.Millisecond {
		t.Fatalf("automatic retry spent its budget inside cache TTL: %v", b.requestTimes)
	}
}
