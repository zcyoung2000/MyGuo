package library

import (
	"myguo/internal/hgapi"
)

func progressUploadPayload(item HistoryItem, episodeID string) map[string]any {
	return hgapi.ProgressUploadPayload(apiProgress(item), episodeID)
}

func favoriteMutationPayload(seriesID string, added bool) map[string]any {
	return hgapi.FavoriteMutationPayload(seriesID, added)
}
func historyDeletePayload(items []HistoryItem, episodeIDs []string) map[string]any {
	return hgapi.HistoryDeletePayload(apiProgressBatch(items), episodeIDs)
}

func decodeRemoteHistoryPage(raw []byte) (*remoteHistoryPage, error) {
	return hgapi.ParseHistoryPage(raw)
}

const favoriteBookType = 2
