package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	userstore "myguo/internal/store"
)

func feedChannelKey(channel string) string {
	if channel == "" {
		return "推荐"
	}
	return channel
}

// RestoreRecommendFeed restores stable metadata and validates the immediate
// lookahead. PlayEpisode checks the remaining restored tail before decoding;
// restarts never reuse a dead localhost port or an expired playback URL.
func (s *HongguoService) RestoreRecommendFeed(ctx context.Context, channel string) (*RecommendFeedResult, error) {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	key := feedChannelKey(channel)
	entries, err := s.store.GetFeedQueue(key)
	if err != nil {
		return nil, err
	}
	progress, _ := s.store.GetFeedProgress(key)
	items := make([]SeriesCard, 0, len(entries))
	for _, entry := range entries {
		var card SeriesCard
		if err := json.Unmarshal(entry.Payload, &card); err != nil {
			return nil, fmt.Errorf("restore recommend queue: %w", err)
		}
		items = append(items, card)
	}
	// Validate the current item and immediate lookahead together. A large old
	// queue can carry thousands of episode rows per work; validating its whole
	// tail here would put an eight-second catalog request before a cached video.
	head := min(3, len(items))
	validationCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	validated := s.validateRecommendCards(validationCtx, channel, items[:head])
	cancel()
	items = append(validated, items[head:]...)
	for i := range items {
		items[i].CoverURL = s.media.ImageURL(items[i].CoverURL)
	}
	return sanitizeIPC(&RecommendFeedResult{Items: items, Cursor: progress.Cursor, HasMore: true}), nil
}

func (s *HongguoService) ConsumeRecommendFeedItem(ctx context.Context, channel, seriesID string) error {
	// Store serializes this with queue writes. Do not wait for feedMu here:
	// a slow background Discover must not delay an on-screen acknowledgement.
	return s.store.ConsumeFeedQueueContext(ctx, feedChannelKey(channel), seriesID)
}

func (s *HongguoService) queueRecommendCards(channel string, cards []SeriesCard) error {
	entries, err := recommendQueueEntries(cards)
	if err != nil {
		return err
	}
	return s.store.AppendFeedQueue(channel, entries)
}

func recommendQueueEntries(cards []SeriesCard) ([]userstore.FeedQueueEntry, error) {
	entries := make([]userstore.FeedQueueEntry, 0, len(cards))
	for _, card := range cards {
		payload, err := json.Marshal(card)
		if err != nil {
			return nil, err
		}
		entries = append(entries, userstore.FeedQueueEntry{SeriesID: card.ID, Payload: payload})
	}
	return entries, nil
}
