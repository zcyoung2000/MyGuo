package playback

import (
	"time"

	"myguo/internal/media"
)

const playerChromeIdleTimeout = 2 * time.Second

// surfaceClick runs in the actor so rapid clicks use the latest state, even
// before the next UI snapshot. In a series player, picture taps only toggle
// chrome. In the recommendation feed, they toggle playback.
func (c *Controller) surfaceClick() {
	if c.state.Mode == Idle {
		return
	}
	if c.state.Panel != "" {
		c.state.Panel = ""
		c.state.Preferences.Clear = false
		c.chromeActivity = time.Now()
		c.system()
		return
	}
	if c.state.Mode == Feed {
		c.state.Preferences.Clear = false
		if c.state.Error != "" {
			c.state.Paused = false
			c.retrySelection()
			return
		}
		if c.state.Loading || !c.state.Status.Ready || !c.state.Status.Displayed {
			// A tap on a poster asks to play; it cannot silently pause a
			// pending source or its quiet retry before a picture is displayed.
			c.setPaused(false)
			return
		}
		c.setPaused(!c.state.Paused && !c.state.Status.Ended)
		return
	}
	c.state.Preferences.Clear = !c.state.Preferences.Clear
	c.chromeActivity = time.Now()
}

func (c *Controller) notePlayerActivity() {
	if c.state.Mode != Series {
		return
	}
	c.markChromeActivity()
	c.state.Preferences.Clear = false
}

func (c *Controller) markChromeActivity() {
	if c.state.Mode != Series {
		return
	}
	c.chromeActivity = time.Now()
}

func (c *Controller) hideIdleChrome(now time.Time) bool {
	if c.state.Mode != Series || c.state.Panel != "" || c.state.Preferences.Clear || c.chromeActivity.IsZero() {
		return false
	}
	if now.Sub(c.chromeActivity) < playerChromeIdleTimeout {
		return false
	}
	c.state.Preferences.Clear = true
	return true
}

func (c *Controller) retrySelection() {
	s := c.current
	if s == nil {
		if c.state.Mode == Feed {
			c.fetchFeed()
		}
		return
	}
	card, n, position := s.card, s.number, c.state.Status.Position
	s.cancel()
	episodeID := s.resolveEpisode
	if s.dto != nil {
		episodeID = s.dto.EpisodeID
	} else if episodeID == "" && s.detail != nil {
		for _, episode := range s.detail.Episodes {
			if episode.Number == n {
				episodeID = episode.ID
				break
			}
		}
	}
	if retry, ok := c.backend.(interface{ RetryPlaybackNow(string) }); ok {
		retry.RetryPlaybackNow(episodeID)
	}
	if s.engine != nil {
		_ = s.engine.Close()
		s.engine = nil // selectSlot must not snapshot a closed native object.
	}
	delete(c.slots, s.key)
	root := c.root
	c.root = media.WithPlaybackRetry(root)
	defer func() { c.root = root }()
	c.selectSlot(card, n, position, 0)
}

func (c *Controller) setPaused(paused bool) {
	c.markChromeActivity()
	s := c.current
	if s != nil {
		s.clockAdvanced = time.Time{}
		if c.state.Paused && !paused && s.engine != nil && !s.first {
			// Paused time is not time spent attempting the first frame.
			s.sourceStarted = time.Now()
		}
	}
	c.state.Paused = paused
	if paused {
		c.state.Preferences.Clear = false
	}
	if !paused && s != nil && s.engine != nil && c.state.Status.Ended {
		c.state.Loading = false
		c.seek(s, 0)
		s.ended = false
		c.state.Status.Ended = false
	}
	if s != nil && s.engine != nil {
		if paused {
			s.engine.Pause()
			c.state.Status = playerStatus(s)
			c.saveCurrent()
		} else {
			s.engine.Play()
		}
	}
	c.system()
}
