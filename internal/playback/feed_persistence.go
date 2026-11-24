package playback

import (
	"context"
	"log"
	"time"

	"myguo/internal/avkit"
)

const feedSaveNotice = "推荐记录暂时无法保存，请稍后重试"

type feedWriteKey struct {
	channel, series string
	scene           uint64
}

type feedWrite struct {
	key          feedWriteKey
	queued, done bool // Protected by progressMu, including retryAt.
	retryAt      time.Time
}

// Readiness alone can precede the visible frame. Preloading must not consume
// a pending card; only the selected surface can acknowledge it.
func (c *Controller) recordFeedStatus(s *slot, status avkit.Status) {
	if c.state.Mode == Feed && s == c.current && s != nil && status.Ready && status.Displayed && status.Error == "" && c.state.Error == "" {
		c.enqueueFeedWrite(c.state.Channel, s.seriesID)
	}
}

func (c *Controller) enqueueFeedWrite(channel, series string) {
	if series == "" {
		return
	}
	if channel == "" {
		channel = "推荐"
	}
	key := feedWriteKey{channel: channel, series: series, scene: c.scene}
	c.progressMu.Lock()
	if c.feedWrites == nil {
		c.feedWrites = make(map[feedWriteKey]*feedWrite)
	}
	w := c.feedWrites[key]
	if w != nil && (w.done || w.queued || time.Now().Before(w.retryAt)) {
		c.progressMu.Unlock()
		return
	}
	if w == nil {
		w = &feedWrite{key: key}
		c.feedWrites[key] = w
	}
	w.queued = true
	c.progressQueue = append(c.progressQueue, progressWrite{feed: w})
	c.progressMu.Unlock()
	select {
	case c.progressWake <- struct{}{}:
	default:
	}
}

// Shares the progress writer so normal shutdown drains both kinds of writes.
// Background contexts let acknowledgements survive feed scene cancellation.
func (c *Controller) saveFeedWrite(w *feedWrite) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = c.backend.ConsumeRecommendFeedItem(ctx, w.key.channel, w.key.series)
		if err == nil || ctx.Err() != nil {
			break
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	c.progressMu.Lock()
	w.queued, w.done = false, err == nil
	w.retryAt = time.Now().Add(5 * time.Second)
	c.progressMu.Unlock()
	if err != nil {
		log.Printf("[native-player] save feed acknowledgement failed: %v", err)
	}
	c.post(func() {
		if c.scene != w.key.scene || c.state.Mode != Feed {
			return
		}
		if err != nil {
			c.state.Notice = feedSaveNotice
		} else if c.state.Notice == feedSaveNotice {
			c.state.Notice = ""
		}
	})
}
