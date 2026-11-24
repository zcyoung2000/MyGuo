package playback

import "time"

// beginLoadingGrace decides whether an episode promotion should suppress the
// loading spinner briefly. Only Windows (loadingGraceEnabled) and a genuinely
// loading promotion opt in; every other case keeps LoadingGrace false so the
// original immediate-spinner behavior is preserved. It is called when the
// selection starts, so the async source negotiation gap is covered as well.
func beginLoadingGrace(loading bool, now time.Time) (bool, time.Time) {
	if !loadingGraceEnabled || !loading {
		return false, time.Time{}
	}
	return true, now
}

// endLoadingGrace clears the suppression as soon as the spinner is no longer
// needed (playback has actually started). publish() then sees a clean state.
func endLoadingGrace() (bool, time.Time) {
	return false, time.Time{}
}

// expireLoadingGrace clears an active grace once its maximum window elapses so
// publish() observes a state change and redraws with the spinner if playback
// never started. No-op on platforms where grace is disabled or no promotion is
// in progress.
func expireLoadingGrace(grace bool, since, now time.Time) (bool, time.Time) {
	if !loadingGraceEnabled || !grace || since.IsZero() {
		return grace, since
	}
	if now.Sub(since) >= loadingGraceWindow {
		return false, time.Time{}
	}
	return grace, since
}
