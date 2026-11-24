//go:build windows

package playback

import "time"

// loadingGraceEnabled gates the auto-advance loading-spinner grace period.
// Windows Media Foundation has no preroll and decodes the promoted episode's
// first frame only after Load, so a short grace hides a sub-second spinner.
const loadingGraceEnabled = true

// loadingGraceWindow is the maximum time a promotion may suppress the spinner
// while waiting for decoded playback to start. The grace normally ends sooner,
// as soon as the clock advances and the spinner is no longer needed.
const loadingGraceWindow = time.Second
