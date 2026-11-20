package hgapi

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type HistoryRow struct {
	SeriesID        string `json:"series_id"`
	VidIndex        int    `json:"vid_index"`
	EpisodeCount    int    `json:"episode_cnt"`
	CurrentPlayTime int64  `json:"current_play_time"`
	LastWatchTime   int64  `json:"last_watch_time"`
}

type HistoryPage struct {
	Items      []HistoryRow
	NextOffset int64
	HasMore    bool
	// Native video progress pages move backwards through watch timestamps.
	TimestampCursor bool
}

// The endpoint returns data_list in an object, or data directly as an array.
// Validate the envelope before accepting an empty result so failures cannot
// accidentally be reconciled as an empty remote history.
func ParseHistoryPage(body []byte) (*HistoryPage, error) {
	var envelope struct {
		Code       *int            `json:"code"`
		Data       json.RawMessage `json:"data"`
		NextOffset int64           `json:"next_offset"`
		HasMore    *bool           `json:"has_more"`
		VideoData  *[]struct {
			SeriesID        string `json:"series_id"`
			VidIndex        int    `json:"vid_index"`
			EpisodeCount    int    `json:"episode_count"`
			CurrentPlayTime int64  `json:"current_play_time"`
			LastWatchTime   int64  `json:"last_watch_time"`
		} `json:"video_data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	data := bytes.TrimSpace(envelope.Data)
	if envelope.VideoData != nil {
		if envelope.Code == nil || *envelope.Code != 0 {
			return nil, fmt.Errorf("历史同步响应无效 (code=%v)", envelope.Code)
		}
		rows := *envelope.VideoData
		page := &HistoryPage{Items: make([]HistoryRow, 0, len(rows)), HasMore: len(rows) >= HistoryPageSize, TimestampCursor: true}
		if envelope.HasMore != nil {
			page.HasMore = *envelope.HasMore
		}
		for _, row := range rows {
			if row.SeriesID == "" || row.VidIndex < 1 || row.CurrentPlayTime < 0 || row.LastWatchTime <= 0 {
				return nil, fmt.Errorf("历史同步返回无效播放记录")
			}
			// These endpoints return a one-based watched episode number; the
			// business-facing HistoryRow keeps its existing zero-based index.
			page.Items = append(page.Items, HistoryRow{SeriesID: row.SeriesID, VidIndex: row.VidIndex - 1, EpisodeCount: row.EpisodeCount, CurrentPlayTime: row.CurrentPlayTime, LastWatchTime: row.LastWatchTime})
			if page.NextOffset == 0 || row.LastWatchTime < page.NextOffset {
				page.NextOffset = row.LastWatchTime
			}
		}
		return page, nil
	}
	if (envelope.Code != nil && *envelope.Code != 0) || len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil, fmt.Errorf("历史同步响应无效 (code=%v)", envelope.Code)
	}
	page := &HistoryPage{NextOffset: envelope.NextOffset}
	if envelope.HasMore != nil {
		page.HasMore = *envelope.HasMore
	}
	if data[0] == '[' {
		if err := json.Unmarshal(data, &page.Items); err != nil {
			return nil, err
		}
	} else {
		var object struct {
			Items      []HistoryRow `json:"data_list"`
			NextOffset int64        `json:"next_offset"`
			HasMore    *bool        `json:"has_more"`
		}
		if err := json.Unmarshal(data, &object); err != nil {
			return nil, err
		}
		page.Items, page.NextOffset = object.Items, object.NextOffset
		if object.HasMore != nil {
			page.HasMore = *object.HasMore
		}
	}
	if page.Items == nil {
		return nil, fmt.Errorf("历史同步响应缺少记录列表")
	}
	return page, nil
}

type FavoritesResponse struct {
	Code    *int `json:"code"`
	HasMore bool `json:"has_more"`
	Data    *struct {
		HasMore        bool `json:"has_more"`
		VideoShelfInfo *[]struct {
			SeriesID   string `json:"series_id"`
			ModifyTime int64  `json:"modify_time"`
		} `json:"video_shelf_info"`
	} `json:"data"`
}

func parseFavoritesResponse(body []byte) (*FavoritesResponse, error) {
	var shelfResp FavoritesResponse

	if err := json.Unmarshal(body, &shelfResp); err != nil {
		return nil, fmt.Errorf("收藏书架响应解析失败: %w", err)
	}
	if shelfResp.Code == nil || *shelfResp.Code != 0 || shelfResp.Data == nil ||
		shelfResp.Data.VideoShelfInfo == nil || shelfResp.HasMore || shelfResp.Data.HasMore {
		return nil, fmt.Errorf("收藏书架返回不完整")
	}
	return &shelfResp, nil
}

type LikesResponse struct {
	Code    *int `json:"code"`
	HasMore bool `json:"has_more"`
	Data    *struct {
		HasMore       bool `json:"has_more"`
		NextOffset    int  `json:"next_offset"`
		MixedDataList []struct {
			SeriesID       string `json:"series_id"`
			BookID         string `json:"book_id"`
			SeriesTitle    string `json:"series_title"`
			SeriesCoverURL string `json:"series_cover_url"`
			SeriesIntro    string `json:"series_intro"`
			VideoData      struct {
				SeriesID    string `json:"series_id"`
				Vid         string `json:"vid"`
				Title       string `json:"title"`
				Cover       string `json:"cover"`
				CoverURL    string `json:"cover_url"`
				ThumbURL    string `json:"thumb_url"`
				Description string `json:"video_desc"`
				EpisodeCnt  int    `json:"episode_cnt"`
				DiggTime    int64  `json:"user_digg_timestamp_ms"`
				CreateTime  int64  `json:"create_time"`
				Detail      struct {
					SeriesTitle string `json:"series_title"`
				} `json:"video_detail"`
			} `json:"video_data"`
		} `json:"mixed_data_list"`
	} `json:"data"`
}

func parseLikesResponse(body []byte) (*LikesResponse, error) {
	var res LikesResponse

	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	if res.Code == nil || *res.Code != 0 || res.Data == nil || res.Data.MixedDataList == nil {
		return nil, fmt.Errorf("点赞接口返回不完整或业务失败")
	}

	return &res, nil
}

type ReservationsResponse struct {
	Code *int `json:"code"`
	Data *struct {
		HasMore    bool `json:"has_more"`
		NextOffset int  `json:"next_offset"`
		Items      []struct {
			ItemID       string `json:"item_id"`
			Name         string `json:"name"`
			SubInfo      string `json:"sub_info"`
			Cover        string `json:"cover"`
			IsOnline     bool   `json:"is_online"`
			Expected     string `json:"expected_rec_text"`
			ScheduleTime int64  `json:"schedule_publish_time"`
			PlannedTime  int64  `json:"expected_publish_time"`
			Row          *struct {
				ID    string `json:"series_id"`
				IDStr string `json:"series_id_str"`
				Title string `json:"title"`
				Cover string `json:"cover"`
			} `json:"subscribe_data"`
		} `json:"subscribe_items"`
	} `json:"data"`
}

func parseReservationsResponse(body []byte) (*ReservationsResponse, error) {
	var res ReservationsResponse

	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	if res.Code == nil || *res.Code != 0 || res.Data == nil || res.Data.Items == nil {
		return nil, fmt.Errorf("预约接口返回不完整或业务失败")
	}

	return &res, nil
}
