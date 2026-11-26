package ipc

import (
	"myguo/internal/hgapi/model"
	"time"
)

const (
	// The SDK verified that 20 IDs are accepted and 30 are rejected, but keeps
	// each batch at 10 so a response stays under roughly 1 MB.
	seriesBatchSize    = 10
	seriesBatchTimeout = 8 * time.Second
)

func seriesBatchIDs(cards []SeriesCard, limit int) []string {
	ids := make([]string, 0, limit)
	seen := make(map[string]bool, limit)
	for _, card := range cards {
		if len(ids) == limit {
			break
		}
		if card.ID == "" || seen[card.ID] {
			continue
		}
		seen[card.ID] = true
		ids = append(ids, card.ID)
	}
	return ids
}

func seriesCardFromDetail(card SeriesCard, detail *model.Series, imageURL func(string) string) SeriesCard {
	if detail == nil {
		return card
	}
	if detail.Title != "" {
		card.Title = detail.Title
	}
	if detail.Description != "" {
		card.Description = detail.Description
	}
	if detail.CoverURL != "" && time.Now().Before(detail.CoverExpiresAt) {
		card.CoverURL = imageURL(detail.CoverURL)
	}
	if detail.EpisodeCount > 0 || isUpcomingRightText(detail.RightText) {
		card.EpisodeCount = detail.EpisodeCount
		card.Upcoming = isUpcomingSeries(detail.EpisodeCount, detail.RightText)
		if card.Upcoming {
			card.Status = "upcoming"
		}
	}
	if detail.Heat > 0 {
		card.Heat = formatHeatScore(detail.Heat)
	}
	return card
}
