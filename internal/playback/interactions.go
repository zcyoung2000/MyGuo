package playback

import "time"

// surfaceClick runs in the actor so rapid clicks use the latest state, even
// before the next UI snapshot. Visible playing -> clean playing -> paused with
// controls; clicking a paused picture resumes clean playback.
func (c *Controller) surfaceClick() {
	if c.state.Mode == Idle {
		return
	}
	if c.state.Panel != "" {
		c.state.Panel = ""
		c.state.Preferences.Clear = false
		c.system()
		return
	}
	if c.state.Error != "" {
		c.state.Preferences.Clear = false
		c.state.Paused = false
		c.retrySelection()
		return
	}
	if c.state.Mode == Feed {
		c.setPaused(true)
		return
	}
	if c.state.Paused || c.state.Status.Ended {
		c.setPaused(false)
		c.state.Preferences.Clear = true
	} else if c.state.Preferences.Clear {
		c.setPaused(true)
	} else {
		c.state.Preferences.Clear = true
	}
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
	if s.engine != nil {
		_ = s.engine.Close()
		s.engine = nil // selectSlot must not snapshot a closed native object.
	}
	delete(c.slots, s.key)
	c.selectSlot(card, n, position, 0)
}

func (c *Controller) setPaused(paused bool) {
	s := c.current
	if s != nil {
		s.clockAdvanced = time.Time{}
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
