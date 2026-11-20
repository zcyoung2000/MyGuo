package playback

import (
	"log"
	"math"
	"time"

	"myguo/internal/avkit"
)

func remainingWallSeconds(s State) float64 {
	duration := s.Status.Duration
	if duration <= 0 && s.Playback != nil {
		duration = float64(s.Playback.DurationSec)
	}
	if duration <= 0 {
		return math.Inf(1)
	}
	return math.Max(0, duration-s.Status.Position) / math.Max(.25, s.Preferences.Rate)
}

func preloadAdmission(s State, admitted bool) bool {
	if !s.Status.Ready || s.Status.Buffering || s.Error != "" || (s.Loading && !s.Paused) {
		return false
	}
	ahead := math.Max(0, s.Status.Buffered-s.Status.Position) / math.Max(.25, s.Preferences.Rate)
	remaining := remainingWallSeconds(s)
	if remaining <= ahead+.25 {
		return true
	}
	if s.Paused {
		return true
	}
	if admitted {
		return ahead >= 2
	}
	return ahead >= 5
}

func (c *Controller) warmNeighbor(candidate *slot) {
	// Keep the current surface, one previous surface and one warm successor.
	var previous *slot
	for _, s := range c.slots {
		if s == c.current || s == candidate || s.engine == nil {
			continue
		}
		if previous == nil || s.used.After(previous.used) {
			previous = s
		}
	}
	for _, s := range c.slots {
		if s != c.current && s != candidate && s != previous && s.engine != nil {
			s.engine.Pause()
			_ = s.engine.Close()
			s.engine = nil
			s.preroll = false
			s.seeked = false
		}
	}
	if candidate.engine == nil && !c.loadEngine(candidate, true) {
		return
	}
	if !candidate.seeked {
		position := candidate.resume
		if position < 0 {
			position = candidate.dto.LastPosition
		}
		// Growing HLS cannot warm a resume beyond the produced prefix. Leave
		// that seek to promotion's existing full-file fallback.
		status := candidate.engine.Snapshot()
		if position <= 0 || candidate.dto.IsFullCached || status.Seekable >= position {
			if math.Abs(status.Position-math.Max(0, position)) > .1 {
				candidate.engine.Seek(position)
				candidate.preroll = false
			}
			candidate.seeked = true
		}
	}
	if !candidate.preroll || !candidate.warming {
		candidate.engine.Preroll()
		candidate.warming = true
		candidate.preroll = candidate.engine.Snapshot().Ready
	}
}

func (c *Controller) traceBuffering(s *slot, status avkit.Status, now time.Time) {
	if !s.first || c.state.Paused {
		s.bufferingSince = time.Time{}
		return
	}
	if status.Buffering {
		if s.bufferingSince.IsZero() {
			s.bufferingSince = now
			log.Printf("[native-player] buffering-start episode=%q position=%.2f ahead=%.2f rate=%.2f", s.dto.EpisodeID, status.Position, math.Max(0, status.Buffered-status.Position), c.state.Preferences.Rate)
		}
	} else if !s.bufferingSince.IsZero() {
		log.Printf("[native-player] buffering-end episode=%q elapsedMs=%d ahead=%.2f", s.dto.EpisodeID, max(0, now.Sub(s.bufferingSince).Milliseconds()), math.Max(0, status.Buffered-status.Position))
		s.bufferingSince = time.Time{}
	}
}
