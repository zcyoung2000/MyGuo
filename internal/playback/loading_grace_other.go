//go:build !windows

package playback

import "time"

// macOS (AVKit) preroll already hides promotion latency, so the loading grace
// period stays disabled and the original immediate-spinner behavior is kept.
const loadingGraceEnabled = false

// loadingGraceWindow is unused when loadingGraceEnabled is false, but kept so
// both platform files expose the same identifiers to shared code.
const loadingGraceWindow = time.Second
