package ipc

import (
	"context"
	"errors"
	"log"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/model"
)

// Catalog counts describe a work, not its current playback availability. Use
// the player's batch detail before admitting cards, and reuse its episodes in
// source negotiation instead of issuing another detail request at selection.
func (s *HongguoService) validateRecommendCards(ctx context.Context, channel string, cards []SeriesCard) []SeriesCard {
	if len(cards) == 0 || (s.client == nil && s.seriesBatch == nil) {
		return cards
	}
	ctx, cancel := context.WithTimeout(ctx, seriesBatchTimeout)
	defer cancel()
	started, accepted := time.Now(), len(cards)
	defer func() {
		log.Printf("[feed-catalog] validated channel=%q candidates=%d accepted=%d elapsedMs=%d", channel, len(cards), accepted, time.Since(started).Milliseconds())
	}()
	batch := s.client.GetSeriesBatch
	if s.seriesBatch != nil {
		batch = s.seriesBatch
	}
	result := make([]SeriesCard, 0, len(cards))
	for start := 0; start < len(cards); start += seriesBatchSize {
		if ctx.Err() != nil {
			result = append(result, cards[start:]...)
			break
		}
		chunk := cards[start:min(start+seriesBatchSize, len(cards))]
		details, err := batch(ctx, seriesBatchIDs(chunk, seriesBatchSize))
		gone := make(map[string]bool)
		deferred := make(map[string]bool)
		if errors.Is(err, hgapi.ErrContentUnavailable) {
			// One removed ID can reject the entire batch. Identify it without
			// deleting the other works returned by this catalog page.
			for _, card := range chunk {
				if len(chunk) == 1 {
					gone[card.ID] = true
					break
				}
				one, oneErr := batch(ctx, []string{card.ID})
				if errors.Is(oneErr, hgapi.ErrContentUnavailable) {
					gone[card.ID] = true
				} else if oneErr == nil {
					details = append(details, one...)
					deferred[card.ID] = len(one) == 0
				} else {
					deferred[card.ID] = true
				}
			}
			if len(gone) > 0 || len(details) > 0 {
				err = nil
			}
		}
		if err != nil || (len(details) == 0 && len(gone) == 0) {
			// An empty/denied batch does not prove every requested work is gone.
			// Preserve these candidates; foreground resolution can recover later.
			log.Printf("[feed-catalog] validation deferred channel=%q candidates=%d error=%v", channel, len(chunk), err)
			result = append(result, chunk...)
			continue
		}
		byID := make(map[string]model.SeriesDetail, len(details))
		for _, detail := range details {
			if detail.Series != nil {
				byID[detail.Series.ID] = detail
			}
		}
		for _, card := range chunk {
			detail, found := byID[card.ID]
			reason := ""
			if gone[card.ID] {
				reason = "content removed"
			} else if !found && len(details) > 0 && !deferred[card.ID] {
				reason = "absent from batch detail"
			} else if found {
				card = seriesCardFromDetail(card, detail.Series, func(raw string) string { return raw })
				playable := make([]model.Episode, 0, len(detail.Episodes))
				for _, episode := range detail.Episodes {
					if episode.Playable && episode.ID != "" && (episode.SeriesID == "" || episode.SeriesID == card.ID) {
						playable = append(playable, episode)
					}
				}
				if card.Upcoming || (len(detail.Episodes) > 0 && len(playable) == 0) {
					reason = "no playable episodes"
				} else if len(playable) > 0 {
					s.episodeMu.Lock()
					s.ensureEpisodeCache()
					s.rememberEpisode(card.ID, &hgapi.Page[model.Episode]{Items: playable}, time.Now())
					s.episodeMu.Unlock()
				}
			}
			if reason != "" {
				log.Printf("[feed-catalog] reject channel=%q series=%q title=%q reason=%q", channel, card.ID, card.Title, reason)
				if err := s.store.RemoveFeedQueueItem(feedChannelKey(channel), card.ID); err != nil {
					log.Printf("[feed-catalog] remove rejected entry: %v", err)
				}
				continue
			}
			result = append(result, card)
		}
	}
	accepted = len(result)
	return result
}
