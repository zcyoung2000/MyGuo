package library

import (
	"context"
	"fmt"
	"strings"
	"time"

	"myguo/internal/mediautil"
	userstore "myguo/internal/store"
)

const seriesProgressTimeout = 8 * time.Second

func (s *Service) ReconcileSeriesProgress(ctx context.Context, seriesID string) (HistoryItem, error) {
	seriesID = strings.TrimSpace(seriesID)
	ctx = s.bindSyncSession(ctx)
	session := ctx.Value(syncSessionContextKey{}).(syncSession)
	s.historyMu.Lock()
	local, _ := s.store.GetHistoryItem(seriesID)
	initial := local
	s.historyMu.Unlock()
	if !mediautil.ValidContentID(seriesID) {
		return local, fmt.Errorf("历史 ID 无效")
	}
	remote, remoteEpisodes, err := s.fetchSeriesProgress(ctx, seriesID)
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	local, hasLocal := s.store.GetHistoryItem(seriesID)
	if !historyItemsEqual(initial, local) || !s.syncSessionCurrent(session) {
		return local, nil
	}
	if err != nil || remote == nil {
		return local, err
	}
	localEpisodes, err := s.store.EpisodeProgress(seriesID, session.scope)
	if err != nil {
		return local, err
	}
	localByNumber := make(map[int]userstore.EpisodeProgress, len(localEpisodes))
	for _, item := range localEpisodes {
		localByNumber[item.EpisodeNum] = item
	}
	for index := range remoteEpisodes {
		older, exists := localByNumber[remoteEpisodes[index].EpisodeNum]
		if exists && older.UpdatedAt > remoteEpisodes[index].UpdatedAt {
			remoteEpisodes[index].PositionSec = older.PositionSec
			remoteEpisodes[index].UpdatedAt = older.UpdatedAt
		}
		if remoteEpisodes[index].DurationSec <= 0 && exists {
			remoteEpisodes[index].DurationSec = older.DurationSec
		}
		remoteEpisodes[index].Watched = remoteEpisodes[index].Watched || exists && older.Watched || remoteEpisodes[index].PositionSec > 0
	}
	if len(remoteEpisodes) > 0 {
		if _, err := s.store.SaveEpisodeProgressBatchIfCurrentScope(remoteEpisodes, session.scope); err != nil {
			return local, err
		}
	}
	merged := mergeProgress(local, *remote)
	if hasLocal {
		merged.Title = local.Title
		merged.CoverURL = local.CoverURL
		merged.Intro = local.Intro
		merged.ContentType = local.ContentType
		if merged.DurationSec <= 0 && merged.EpisodeNum == local.EpisodeNum {
			merged.DurationSec = local.DurationSec
		}
	}
	if !hasLocal {
		merged = enrichProgress(merged, HistoryItem{})
	}
	if !hasLocal || !historyItemsEqual(local, merged) {
		applied, err := s.store.SaveHistoryIfCurrentScope(merged, session.scope)
		if err != nil {
			return local, err
		}
		if !applied {
			current, _ := s.store.GetHistoryItem(seriesID)
			return current, nil
		}
		s.cachedHistory = nil
		s.historyCachedAt = time.Time{}
		s.historyGeneration++
	}
	return merged, nil
}

func historyItemsEqual(left, right HistoryItem) bool {
	return left.SeriesID == right.SeriesID &&
		left.EpisodeNum == right.EpisodeNum &&
		left.PositionSec == right.PositionSec &&
		left.DurationSec == right.DurationSec &&
		left.UpdatedAt == right.UpdatedAt
}

func mergeProgress(local, remote HistoryItem) HistoryItem {
	if local.SeriesID == "" {
		return remote
	}
	if remote.SeriesID == "" {
		return local
	}
	if local.UpdatedAt != remote.UpdatedAt {
		if local.UpdatedAt > remote.UpdatedAt {
			return enrichProgress(local, remote)
		}
		return enrichProgress(remote, local)
	}
	result := local
	if remote.EpisodeNum > local.EpisodeNum {
		result = remote
	} else if remote.EpisodeNum == local.EpisodeNum && remote.PositionSec > local.PositionSec {
		result = remote
	}
	return enrichProgress(result, olderProgress(local, remote))
}

func olderProgress(local, remote HistoryItem) HistoryItem {
	if local.UpdatedAt <= remote.UpdatedAt {
		return local
	}
	return remote
}

func enrichProgress(newer, older HistoryItem) HistoryItem {
	if newer.Title == "" {
		newer.Title = older.Title
	}
	if newer.CoverURL == "" {
		newer.CoverURL = older.CoverURL
	}
	if newer.TotalEpisodes <= 0 {
		newer.TotalEpisodes = older.TotalEpisodes
	}
	if newer.DurationSec <= 0 && newer.EpisodeNum == older.EpisodeNum {
		newer.DurationSec = older.DurationSec
	}
	if newer.Intro == "" {
		newer.Intro = older.Intro
	}
	if newer.ContentType == 0 {
		newer.ContentType = older.ContentType
	}
	return newer
}

func (s *Service) fetchSeriesProgress(ctx context.Context, seriesID string) (*HistoryItem, []userstore.EpisodeProgress, error) {
	ctx = s.bindSyncSession(ctx)
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return nil, nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, seriesProgressTimeout)
	defer cancel()
	page, err := s.apiForSession(session).History(ctx, 0, seriesID)
	if err != nil {
		return nil, nil, err
	}
	var latest *HistoryItem
	byEpisode := make(map[int]userstore.EpisodeProgress)
	for _, row := range page.Items {
		if row.SeriesID != seriesID {
			continue
		}
		episodeNum := row.VidIndex + 1
		if episodeNum <= 0 {
			continue
		}
		updatedAt := remoteProgressUpdatedAt(row)
		episode := userstore.EpisodeProgress{
			SeriesID: seriesID, EpisodeNum: episodeNum,
			PositionSec: float64(row.CurrentPlayTime) / 1000, UpdatedAt: updatedAt,
		}
		if current, ok := byEpisode[episodeNum]; !ok || episode.UpdatedAt >= current.UpdatedAt {
			byEpisode[episodeNum] = episode
		}
		item := HistoryItem{
			SeriesID:      row.SeriesID,
			Title:         "未知短剧",
			EpisodeNum:    episodeNum,
			EpisodeTitle:  fmt.Sprintf("第%d集", episodeNum),
			PositionSec:   float64(row.CurrentPlayTime) / 1000,
			TotalEpisodes: row.EpisodeCount,
			UpdatedAt:     updatedAt,
			ContentType:   1004,
		}
		if latest == nil || item.UpdatedAt >= latest.UpdatedAt {
			latest = &item
		}
	}
	if latest == nil {
		return nil, nil, nil
	}
	episodes := make([]userstore.EpisodeProgress, 0, len(byEpisode))
	for _, item := range byEpisode {
		episodes = append(episodes, item)
	}
	return latest, episodes, nil
}

func remoteProgressUpdatedAt(row remoteHistoryRow) int64 {
	if row.LastWatchTime <= 0 {
		return time.Now().Unix()
	}
	updatedAt := row.LastWatchTime / 1000
	if updatedAt <= 0 {
		return row.LastWatchTime
	}
	return updatedAt
}
