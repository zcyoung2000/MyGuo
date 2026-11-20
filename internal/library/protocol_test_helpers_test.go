package library

import (
	"myguo/internal/hgapi"
)

func historyUpdatePayload(item HistoryItem, episodeID string) map[string]any {
	return hgapi.HistoryUpdatePayload(apiProgress(item), episodeID)
}
func historyUpdateBatchPayload(items []HistoryItem, episodeIDs []string) map[string]any {
	return hgapi.HistoryUpdateBatchPayload(apiProgressBatch(items), episodeIDs)
}
func progressUploadPayload(item HistoryItem, episodeID string) map[string]any {
	return hgapi.ProgressUploadPayload(apiProgress(item), episodeID)
}
func progressUploadBatchPayload(items []HistoryItem, episodeIDs []string) map[string]any {
	return hgapi.ProgressUploadBatchPayload(apiProgressBatch(items), episodeIDs)
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
