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
	if !status.Buffering && status.Playing && s.playStarted {
		return false
	}
	stalled := now.Sub(s.clockAdvanced)
	resumeAfter, switchAfter := 3*time.Second, 8*time.Second
	bufferedStart := !s.playStarted && !status.PictureInPicture && status.Buffered-status.Position >= math.Max(2, c.state.Preferences.Rate*2)
	if bufferedStart {
		// A decoded first frame with a playable runway must not wait through
		// the rebuffering budget. Retry the native start once, then use the
		// already buffered HLS asset once before waiting for a full download.
		resumeAfter, switchAfter = 700*time.Millisecond, 2*time.Second
	}
	if stalled >= switchAfter {
		if bufferedStart && !s.startReloaded && s.dto != nil && s.sourceURL == s.dto.HLSStreamURL {
			s.startReloaded = true
			log.Printf("[native-player] restart buffered startup episode=%q position=%.2f ahead=%.2f", s.dto.EpisodeID, status.Position, status.Buffered-status.Position)
			// The first live prefix may have left AVKit stopped at its first
			// frame. Reopen the same local playlist after it has a runway; the
			// media job, model and rendition stay cached, with no new decoder.
			if err := s.engine.Load(s.sourceURL); err == nil {
				s.first, s.playStarted, s.seekPending = false, false, false
				s.clockAdvanced = time.Time{}
				s.sourceStarted = now
				c.state.Loading = true
				c.state.Status = avkit.Status{}
				if status.Position > 0 {
					c.seek(s, status.Position)
				}
				c.configure(s)
				s.engine.Play()
				return true
			}
		}
		log.Printf("[native-player] recovering stalled stream episode=%q position=%.2f duration=%.2f ahead=%.2f elapsedMs=%d", s.dto.EpisodeID, status.Position, status.Duration, math.Max(0, status.Buffered-status.Position), stalled.Milliseconds())
		c.fallback(s, "playback clock stopped")
		return true
	}
	if stalled >= resumeAfter && !s.stallResumed && (!status.Playing || status.Buffered-status.Position >= math.Max(.5, c.state.Preferences.Rate*.5)) {
		// AVPlayer may retain playable bytes after entering a wait state. Give
		// it one resume attempt before spending bandwidth on another transport.
		s.stallResumed = true
		log.Printf("[native-player] resume buffered stall episode=%q position=%.2f", s.dto.EpisodeID, status.Position)
		s.engine.Play()
	}
	return false
}
