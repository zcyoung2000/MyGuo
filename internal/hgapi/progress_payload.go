package hgapi

import (
	"fmt"
	"strconv"
	"time"
)

const favoriteBookType = 2

// ProgressRecord carries application units; only API encoding converts seconds
// to protocol milliseconds and episode numbers to zero-based indexes.
type ProgressRecord struct {
	SeriesID                 string
	EpisodeNum               int
	PositionSec, DurationSec float64
	TotalEpisodes            int
}

func contentInt(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("内容 ID 无效")
	}
	return parsed, nil
}

func HistoryUpdatePayload(item ProgressRecord, episodeID string) map[string]any {
	bookID, err := contentInt(item.SeriesID)
	if err != nil {
		return nil
	}
	episodeNum, err := contentInt(episodeID)
	if err != nil {
		return nil
	}
	index := item.EpisodeNum - 1
	if index < 0 {
		index = 0
	}
	now := time.Now().UnixMilli()
	positionMS := int64(item.PositionSec * 1000)
	inner := map[string]any{
		"book_id":                      bookID,
		"book_type":                    favoriteBookType,
		"vid":                          episodeNum,
		"vid_index":                    index,
		"chapter_index":                index,
		"current_play_position":        positionMS,
		"player_accumulate_total_time": positionMS,
		"read_timestamp_ms":            now,
		"update_timestamp_ms":          now,
		"is_delete":                    false,
		"is_interactive_game":          false,
		"is_listen_mode":               false,
		"is_multi_season":              false,
		"season_index":                 0,
		"digged_count":                 0,
		"user_digg":                    false,
		"duration":                     int64(item.DurationSec),
		"episode_cnt":                  item.TotalEpisodes,
		"origin_novel_book_id":         0,
		"recent_reads":                 0,
		"retain_video_play_time":       0,
		"series_play_cnt":              0,
		"tone_id":                      0,
		"use_soft_delete":              false,
		"meet_guide_comment_tag":       false,
		"user_playlet_comment_flag":    false,
	}
	return map[string]any{"update_datas": []any{inner}}
}

func HistoryUpdateBatchPayload(items []ProgressRecord, episodeIDs []string) map[string]any {
	if len(items) != len(episodeIDs) {
		return nil
	}
	updates := make([]any, 0, len(items))
	for i, item := range items {
		payload := HistoryUpdatePayload(item, episodeIDs[i])
		if payload == nil {
			return nil
		}
		updates = append(updates, payload["update_datas"].([]any)[0])
	}
	return map[string]any{"update_datas": updates}
}

func ProgressUploadPayload(item ProgressRecord, episodeID string) map[string]any {
	if _, err := contentInt(item.SeriesID); err != nil {
		return nil
	}
	if _, err := contentInt(episodeID); err != nil {
		return nil
	}
	index := item.EpisodeNum - 1
	if index < 0 {
		index = 0
	}
	now := time.Now().UnixMilli()
	positionMS := int64(item.PositionSec * 1000)
	inner := map[string]any{
		"book_id":                          item.SeriesID,
		"book_type":                        favoriteBookType,
		"item_id":                          episodeID,
		"vid_index":                        index,
		"current_play_time":                positionMS,
		"player_cumulative_total_duration": positionMS,
		"read_timestamp_ms":                now,
		"progress_type":                    0,
		"channel_id":                       0,
		"cur_channel_id":                   0,
		"check_timestamp":                  false,
		"is_listen_mode":                   false,
		"is_local_book":                    false,
		"listen_and_read":                  false,
		"page_index":                       0,
		"page_progress_rate":               0,
		"paragraph_offset":                 0,
		"tone_id":                          0,
	}
	return map[string]any{"books": []any{inner}}
}

func ProgressUploadBatchPayload(items []ProgressRecord, episodeIDs []string) map[string]any {
	if len(items) != len(episodeIDs) {
		return nil
	}
	books := make([]any, 0, len(items))
	for i, item := range items {
		payload := ProgressUploadPayload(item, episodeIDs[i])
		if payload == nil {
			return nil
		}
		books = append(books, payload["books"].([]any)[0])
	}
	return map[string]any{"books": books}
}

func FavoriteMutationPayload(seriesID string, added bool) map[string]any {
	if _, err := contentInt(seriesID); err != nil {
		return nil
	}
	return map[string]any{
		"is_cancelled": false,
		"shark_param": map[string]any{
			"enter_from":         "MainFragmentActivity",
			"previous_page":      "",
			"page_list":          "MainFragmentActivity",
			"is_active_behavior": "true",
			"inactive_type":      "0",
		},
		"book_id":     seriesID,
		"book_type":   favoriteBookType,
		"modify_time": time.Now().UnixMilli(),
		"update_bookshelf_video_list": []any{map[string]any{
			"is_cancelled": !added,
		}},
	}
}

func HistoryDeletePayload(items []ProgressRecord, episodeIDs []string) map[string]any {
	if len(items) != len(episodeIDs) {
		return nil
	}
	updates := make([]any, 0, len(items))
	now := time.Now().UnixMilli()
	for i, item := range items {
		bookID, err := contentInt(item.SeriesID)
		if err != nil {
			continue
		}
		episodeID, err := contentInt(episodeIDs[i])
		if err != nil {
			continue
		}
		index := item.EpisodeNum - 1
		if index < 0 {
			index = 0
		}
		updates = append(updates, map[string]any{
			"book_id":                      bookID,
			"book_type":                    favoriteBookType,
			"vid":                          episodeID,
			"vid_index":                    index,
			"is_delete":                    true,
			"use_soft_delete":              true,
			"update_timestamp_ms":          now,
			"chapter_index":                index,
			"current_play_position":        0,
			"player_accumulate_total_time": 0,
			"read_timestamp_ms":            now,
			"is_interactive_game":          false,
			"is_listen_mode":               false,
			"is_multi_season":              false,
			"season_index":                 0,
			"digged_count":                 0,
			"user_digg":                    false,
			"duration":                     0,
			"episode_cnt":                  item.TotalEpisodes,
			"origin_novel_book_id":         0,
			"recent_reads":                 0,
			"retain_video_play_time":       0,
			"series_play_cnt":              0,
			"tone_id":                      0,
			"meet_guide_comment_tag":       false,
			"user_playlet_comment_flag":    false,
		})
	}
	return map[string]any{"update_datas": updates}
}
