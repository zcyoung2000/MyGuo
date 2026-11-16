package playback

import (
	"context"
	"fmt"
	"myguo/internal/ipc"
	"myguo/internal/media"
	"time"
)

func (c *Controller) appendFeed(items []ipc.SeriesCard) {
	for _, card := range items {
		if card.ID != "" && !card.Upcoming && !c.feedSeen[card.ID] {
			c.feedSeen[card.ID] = true
			c.feed = append(c.feed, card)
		}
	}
}
func sourceLoadMessage(s *slot) string {
	if s.unavailable {
		return "该剧暂未提供可播放剧集"
	}
	return "视频暂时无法加载，请稍后重试"
}

// Persisted queues can contain zero-episode works. Remove only a confirmed
// unavailable item; transport/auth errors must not silently skip a work.
func (c *Controller) skipUnavailableFeed(s *slot) {
	if c.current != s || c.state.Index < 0 || c.state.Index >= len(c.feed) || c.feed[c.state.Index].ID != s.seriesID {
		return
	}
	index := c.state.Index
	c.feed = append(c.feed[:index], c.feed[index+1:]...)
	s.cancel()
	delete(c.slots, s.key)
	c.current = nil
	c.state.Engine = nil
	c.state.Playback = nil
	c.state.Loading = true
	c.state.Error = ""
	channel, id, ctx := c.state.Channel, s.seriesID, c.root
	go func() { _ = c.backend.ConsumeRecommendFeedItem(ctx, channel, id) }()
	if index < len(c.feed) {
		c.selectFeed(index)
	} else if c.state.HasMore {
		c.pendingIndex = index
		c.fetchFeed()
	} else {
		c.state.Loading = false
		c.state.Error = "暂无可播放的推荐，请切换频道"
	}
}

func (c *Controller) fetchFeed() {
	if c.feedLoading || !c.state.HasMore {
		return
	}
	c.feedLoading = true
	scene, channel, cursor, ctx := c.scene, c.state.Channel, c.cursor, c.root
	go func() {
		page, err := c.backend.GetRecommendFeed(ctx, channel, cursor)
		c.post(func() {
			if c.scene != scene || ctx.Err() != nil {
				return
			}
			c.feedLoading = false
			if err != nil || page == nil {
				c.state.Loading = false
				if c.current == nil || len(c.feed) == 0 {
					c.state.Error = "推荐加载失败，请重试"
				} else {
					c.state.Notice = "更多推荐加载失败，可稍后重试"
				}
				return
			}
			c.state.Error = ""
			old := len(c.feed)
			c.appendFeed(page.Items)
			c.state.HasMore = page.HasMore && page.Cursor != "" && page.Cursor != cursor && !c.feedCursors[page.Cursor]
			if page.Cursor != "" {
				c.feedCursors[page.Cursor] = true
				c.cursor = page.Cursor
			}
			if c.current == nil && c.pendingIndex < 0 && len(c.feed) > 0 {
				c.selectFeed(0)
			}
			if len(c.feed) == 0 {
				c.state.Loading = false
				c.state.Error = "暂无可播放的推荐，请切换频道"
			}
			if c.pendingIndex >= 0 && c.pendingIndex < len(c.feed) {
				target := c.pendingIndex
				c.pendingIndex = -1
				c.selectFeed(target)
			}
			if c.pendingIndex >= len(c.feed) && c.current == nil {
				if c.state.HasMore {
					c.state.Loading = true
					c.fetchFeed()
				} else {
					c.pendingIndex = -1
					c.state.Loading = false
					c.state.Error = "暂无可播放的推荐，请切换频道"
				}
			} else if !c.state.HasMore && c.pendingIndex >= len(c.feed) {
				c.pendingIndex = -1
				c.state.Notice = "已经看完当前推荐"
			}
			if len(c.feed) > old {
				c.prepareNeighbors()
			}
		})
	}()
}
func (c *Controller) selectFeed(index int) {
	if c.state.Mode != Feed {
		return
	}
	if index < 0 {
		c.state.Notice = "已经是第一部"
		return
	}
	if index >= len(c.feed) {
		if c.state.HasMore {
			c.pendingIndex = index
			c.fetchFeed()
			c.state.Notice = "正在加载更多推荐"
		} else {
			c.state.Notice = "已经看完当前推荐"
		}
		return
	}
	c.pendingIndex = -1
	direction := index - c.state.Index
	c.state.Index = index
	c.selectSlot(c.feed[index], 1, -1, direction)
	if len(c.feed)-index <= 4 {
		c.fetchFeed()
	}
}
func (c *Controller) prepareNeighbors() {
	if c.current == nil || c.state.Error != "" {
		return
	}
	if c.state.Mode == Feed {
		for i := c.state.Index + 1; i < len(c.feed) && i <= c.state.Index+2; i++ {
			c.resolve(c.feed[i], 1, -1, "")
		}
	}
	if c.current != nil {
		n, total := c.current.number, c.state.Book.EpisodeCount
		lookahead := 1
		if c.state.Mode == Series {
			lookahead = 2
		}
		for next := n + 1; total > 0 && next <= total && next <= n+lookahead; next++ {
			c.resolve(c.current.card, next, 0, "")
		}
	}
	c.trimSlots()
}
func (c *Controller) trimSlots() {
	keep := map[string]bool{c.desired: true}
	key := func(id string, num int) string { return fmt.Sprintf("%s/%d/%s", id, num, c.state.Preferences.Quality) }
	if c.state.Mode == Feed {
		for i := max(0, c.state.Index-1); i < len(c.feed) && i <= c.state.Index+2; i++ {
			keep[key(c.feed[i].ID, 1)] = true
		}
	} else if c.current != nil {
		keep[key(c.current.seriesID, c.current.number-1)] = true
		keep[key(c.current.seriesID, c.current.number+1)] = true
		keep[key(c.current.seriesID, c.current.number+2)] = true
	}
	if c.current != nil {
		keep[key(c.current.seriesID, c.current.number-1)] = true
		keep[key(c.current.seriesID, c.current.number+1)] = true
	}
	for k, s := range c.slots {
		if !keep[k] {
			s.cancel()
			if s.engine != nil {
				s.engine.Pause()
				_ = s.engine.Close()
			}
			delete(c.slots, k)
		}
	}
}
func (c *Controller) updatePreload() {
	if c.preloadLoading || c.current == nil || c.current.dto == nil {
		return
	}
	c.lastPreload = time.Now()
	s := c.current
	items := make([]media.FeedPreloadItem, 0, 2)
	appendItem := func(id string, num int) {
		for _, slot := range c.slots {
			if slot.seriesID == id && slot.number == num && slot.dto != nil && slot != s {
				d := slot.dto
				items = append(items, media.FeedPreloadItem{SeriesID: id, EpisodeID: d.EpisodeID, Number: d.EpisodeNum, Quality: d.Quality})
				break
			}
		}
	}
	// Hysteresis avoids repeatedly deleting/restarting the next rendition as
	// the current range crosses one threshold. A fully buffered tail is safe
	// even when fewer seconds remain than the normal admission threshold.
	budget := c.state
	if s.engine != nil {
		budget.Status = playerStatus(s)
	}
	c.preloadAdmitted = preloadAdmission(budget, c.preloadAdmitted)
	if !c.preloadAdmitted {
		// Stop paused native EVENT polling before canceling the disk job,
		// otherwise its next playlist request can resurrect that job.
		for _, candidate := range c.slots {
			if candidate != s && candidate.engine != nil && candidate.warming {
				candidate.engine.Pause()
				candidate.warming = false
			}
		}
	}
	if c.preloadAdmitted {
		nearEnd := remainingWallSeconds(budget) <= 30
		if c.state.Mode == Series || nearEnd {
			appendItem(s.seriesID, s.number+1)
		}
		if c.state.Mode == Feed {
			for i := c.state.Index + 1; i < len(c.feed) && i <= c.state.Index+2 && len(items) < 2; i++ {
				appendItem(c.feed[i].ID, 1)
			}
		} else {
			appendItem(s.seriesID, s.number+2)
		}
	}
	c.preloadVersion++
	version, scene, ctx, key := c.preloadVersion, c.scene, c.root, c.desired
	c.preloadLoading = true
	currentEpisode, currentQuality := s.dto.EpisodeID, s.dto.Quality
	go func() {
		statuses, _ := c.backend.UpdateFeedPreloadWindow(ctx, version, currentEpisode, currentQuality, items)
		c.post(func() {
			if c.scene != scene || ctx.Err() != nil {
				return
			}
			c.preloadLoading = false
			if c.desired != key {
				return
			}
			// Only the first admitted item gets a native decoder. Other
			// lookahead stays on disk, keeping the three-decoder budget.
			if len(items) == 0 {
				return
			}
			for _, status := range statuses {
				if status.EpisodeID != items[0].EpisodeID || (!status.Complete && status.CachedSeconds < 3) {
					continue
				}
				for _, candidate := range c.slots {
					if candidate == c.current || candidate.dto == nil || candidate.dto.EpisodeID != status.EpisodeID || candidate.dto.Quality != status.Quality {
						continue
					}
					c.warmNeighbor(candidate)
					return
				}
			}
		})
	}()
}

// WaitIdle is a synchronization point for shutdown and deterministic controller
// tests; it does not wait for network requests or for a video to finish loading.
func (c *Controller) WaitIdle(ctx context.Context) error {
	ready := make(chan struct{})
	if !c.post(func() { c.publish(); close(ready) }) {
		return context.Canceled
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ready:
		return nil
	}
}
