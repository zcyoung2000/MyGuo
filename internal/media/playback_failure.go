package media

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/model"
)

const (
	playbackFailureTTL       = 5 * time.Second
	playbackFailureThreshold = 8
	playbackFailureCooldown  = 15 * time.Second
)

// The controller can honor this optional error interface without depending on
// a concrete backend. Unwrap preserves authentication/content classification.
type playbackRetryError struct {
	cause error
	until time.Time
}

func (e *playbackRetryError) Error() string { return e.cause.Error() }
func (e *playbackRetryError) Unwrap() error { return e.cause }
func (e *playbackRetryError) PlaybackRetryAfter() time.Duration {
	return max(0, time.Until(e.until))
}

func playbackFailureRecoverable(err error) bool {
	if err == nil {
		return false
	}
	// Terminal causes take precedence even in a joined race/fallback error.
	for _, terminal := range []error{
		context.Canceled, context.DeadlineExceeded,
		hgapi.ErrAuthenticationRequired, hgapi.ErrDRMProtected,
		hgapi.ErrContentUnavailable, hgapi.ErrVideoTabEpisodeMissing,
		errPlaybackModelUnavailable, hgapi.ErrSignerUnavailable,
		hgapi.ErrAlbumUnavailable, errNilPlaybackClient,
		hgapi.ErrSignatureRejected, hgapi.ErrChallengeRequired,
		hgapi.ErrParamInvalid, hgapi.ErrInvalidOptions,
	} {
		if errors.Is(err, terminal) {
			return false
		}
	}
	if hgapi.Retryable(err) || errors.Is(err, hgapi.ErrAnonymousLimited) ||
		errors.Is(err, hgapi.ErrDeviceRejected) {
		return true
	}
	var network net.Error
	if errors.As(err, &network) {
		return true
	}
	var code *hgapi.CodeError
	return errors.As(err, &code) && code.Code == 110001
}

// Lookahead shares foreground flights and both cache outcomes. A batch only
// requests uncached, unowned episodes; its failures count per actual episode
// load, exactly as foreground loads do. Publication stays in finishPlaybackLoad
// so an invalidated batch cannot restore a stale model via a later callback.
func (ms *MediaServer) loadPlaybackModels(ctx context.Context, episodes []model.Episode) {
	ms.pbLoadMu.Lock()
	if ctx.Err() != nil || time.Now().Before(ms.pbCooldownUntil) {
		ms.pbLoadMu.Unlock()
		return
	}
	if ms.pbLoads == nil {
		ms.pbLoads = make(map[string]*playbackLoad)
	}
	loads := make(map[string]*playbackLoad)
	requested := make([]model.Episode, 0, len(episodes))
	ms.pbCacheMu.RLock()
	for _, episode := range episodes {
		if episode.ID == "" || ms.pbLoads[episode.ID] != nil {
			continue
		}
		if item, cached := ms.pbCache[episode.ID]; cached && time.Now().Before(item.expiresAt) {
			continue
		}
		load := &playbackLoad{done: make(chan struct{})}
		ms.pbLoads[episode.ID] = load
		loads[episode.ID] = load
		requested = append(requested, episode)
	}
	ms.pbCacheMu.RUnlock()
	ms.pbLoadMu.Unlock()
	if len(requested) == 0 {
		return
	}
	playbacks, err := ms.multiModel.fetch(ctx, requested)
	for _, episode := range requested {
		pb, loadErr := playbacks[episode.ID], err
		if ctx.Err() != nil {
			pb, loadErr = nil, ctx.Err()
		} else if loadErr == nil && pb == nil {
			loadErr = hgapi.ErrAnonymousLimited
		}
		ms.finishPlaybackLoad(episode.ID, loads[episode.ID], pb, loadErr)
	}
}

// Caller owns pbLoadMu: only the completed, current flight counts, never its
// waiters, cache hits or cooldown rejections. Success resets the consecutive
// counter (our policy); a triggered cooldown keeps its original deadline.
func (ms *MediaServer) rememberPlaybackFailure(episodeID string, cause error) error {
	now := time.Now()
	err := &playbackRetryError{cause: cause, until: now.Add(playbackFailureTTL)}
	expiresAt := err.until
	ms.pbConsecutiveFailures++
	if ms.pbConsecutiveFailures >= playbackFailureThreshold {
		ms.pbConsecutiveFailures = 0
		ms.pbCooldownUntil = now.Add(playbackFailureCooldown)
	}
	ms.pbCacheMu.Lock()
	ms.rememberPlaybackCacheLocked(episodeID, playbackCacheItem{err: err, expiresAt: expiresAt})
	ms.pbCacheMu.Unlock()
	return &playbackRetryError{cause: cause, until: maxTime(err.until, ms.pbCooldownUntil)}
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func (ms *MediaServer) cachedPlaybackError(err error) error {
	if retry, ok := err.(*playbackRetryError); ok {
		ms.pbLoadMu.Lock()
		until := maxTime(retry.until, ms.pbCooldownUntil)
		ms.pbLoadMu.Unlock()
		return &playbackRetryError{cause: retry.cause, until: until}
	}
	return err
}

type playbackRetryKey struct{}
type playbackRetryIntent struct{ once sync.Once }

// WithPlaybackRetry invalidates the first episode actually resolved through
// this context, once per manual action. IPC can discover its ID and derive
// timeout contexts without needing a new mandatory Backend method. Automatic
// retries using the same slot context do not repeatedly bypass throttling.
func WithPlaybackRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, playbackRetryKey{}, &playbackRetryIntent{})
}

func (ms *MediaServer) applyPlaybackRetry(ctx context.Context, episodeID string) {
	if intent, ok := ctx.Value(playbackRetryKey{}).(*playbackRetryIntent); ok {
		intent.once.Do(func() { ms.RetryPlaybackNow(episodeID) })
	}
}

// RetryPlaybackNow invalidates this episode's model (success or failure) and
// flight, then resets global throttling. Existing flight waiters retain their
// result; stale completions cannot undo the retry. Complete media files and
// unrelated media model entries remain reusable.
func (ms *MediaServer) RetryPlaybackNow(episodeID string) {
	resolver := ms.playbackResolver
	if resolver == nil && ms.client != nil {
		resolver = newNativeFallbackPlaybackResolver(ms.client, nil)
	}
	if retry, ok := resolver.(interface{ RetryPlaybackNow(string) }); ok {
		retry.RetryPlaybackNow(episodeID)
	}
	ms.pbLoadMu.Lock()
	defer ms.pbLoadMu.Unlock()
	delete(ms.pbLoads, episodeID)
	ms.pbCacheMu.Lock()
	delete(ms.pbCache, episodeID)
	ms.pbCacheMu.Unlock()
	ms.pbConsecutiveFailures = 0
	ms.pbCooldownUntil = time.Time{}
}
