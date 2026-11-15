package playback

import (
	"log"
	"math"
	"time"

	"myguo/internal/avkit"
)

func (c *Controller) incompleteHLSEnd(s *slot, status avkit.Status) bool {
	return status.Ended && c.state.Error == "" && s.dto != nil && !s.dto.IsFullCached &&
		s.sourceURL == s.dto.HLSStreamURL && s.dto.DurationSec > 0 &&
		status.Position+2 < float64(s.dto.DurationSec)
}

// A successful first frame does not guarantee that an EVENT stream will keep
// producing segments. Detect an unmoving clock, rather than the duration of a
// potentially flickering buffering flag. Never infer EOF from a partial prefix.
func (c *Controller) recoverStall(s *slot, status avkit.Status, now time.Time) bool {
	if !s.first || !status.Ready || status.Ended || s.seekPending || c.state.Paused || c.state.Error != "" {
		s.clockAdvanced = time.Time{}
		return false
	}
	if s.clockAdvanced.IsZero() || math.Abs(status.Position-s.clockPosition) > .05 {
		s.clockPosition, s.clockAdvanced, s.stallResumed = status.Position, now, false
		return false
	}
	if !status.Buffering {
		return false
	}
	stalled := now.Sub(s.clockAdvanced)
	if stalled >= 8*time.Second {
		log.Printf("[native-player] recovering stalled stream episode=%q position=%.2f duration=%.2f ahead=%.2f elapsedMs=%d", s.dto.EpisodeID, status.Position, status.Duration, math.Max(0, status.Buffered-status.Position), stalled.Milliseconds())
		c.fallback(s, "playback clock stopped while buffering")
		return true
	}
	if stalled >= 3*time.Second && !s.stallResumed && status.Buffered-status.Position >= math.Max(.5, c.state.Preferences.Rate*.5) {
		// AVPlayer may retain playable bytes after entering a wait state. Give
		// it one resume attempt before spending bandwidth on another transport.
		s.stallResumed = true
		log.Printf("[native-player] resume buffered stall episode=%q position=%.2f", s.dto.EpisodeID, status.Position)
		s.engine.Play()
	}
	return false
}
