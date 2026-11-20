package hgapi

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
)

const HistoryPageSize = 50

func (c *SessionClient) History(ctx context.Context, offset int64, series string) (*HistoryPage, error) {
	if err := c.requireAccount(); err != nil {
		return nil, err
	}
	if offset < 0 || (series != "" && !validContentID(series)) {
		return nil, ErrParamInvalid
	}
	var body []byte
	var err error
	if series != "" {
		id, _ := strconv.ParseInt(series, 10, 64)
		raw, _ := json.Marshal(map[string]any{"book_ids": map[string][]int64{"2": {id}}})
		compressed, err := GzipBody(raw)
		if err != nil {
			return nil, err
		}
		body, err = c.call(ctx, endpoint{"单剧进度", accountOrigin, "/reading/bookapi/read_progress/get/v", "POST", postSigned, 4 << 20}, c.devicePairs(), compressed, map[string]string{"Content-Encoding": "gzip", "X-SS-DP": "8662", "Lc": "101"})
	} else {
		cursor := offset
		if cursor == 0 {
			cursor = time.Now().UnixMilli()
		}
		extra := [][2]string{{"book_types", "2"}, {"limit", strconv.Itoa(HistoryPageSize)}, {"last_min_read_timestamp_ms_list", strconv.FormatInt(cursor, 10)}}
		body, err = c.call(ctx, endpoint{"历史同步", accountOrigin, "/reading/bookapi/read_progress/list/v", "GET", getSigned, 4 << 20}, c.devicePairs(extra...), nil, nil)
	}
	if err != nil {
		return nil, err
	}
	page, err := ParseHistoryPage(body)
	if err != nil {
		return nil, err
	}
	if series != "" {
		page.HasMore = false
	}
	if page.TimestampCursor && page.HasMore && (page.NextOffset <= 0 || (offset > 0 && page.NextOffset >= offset)) {
		return nil, ErrParamInvalid
	}
	if !page.TimestampCursor && page.HasMore && page.NextOffset <= offset {
		page.NextOffset = offset + int64(len(page.Items))
	}
	return page, nil
}

func (c *SessionClient) Favorites(ctx context.Context) (*FavoritesResponse, error) {
	if err := c.requireAccount(); err != nil {
		return nil, err
	}
	s := c.session
	pairs := [][2]string{{"aid", "8662"}, {"app_name", "novelread"}, {"version_code", "73932"}, {"device_platform", "android"}, {"device_id", s.DeviceID}, {"iid", s.InstallID}}
	body, err := c.call(ctx, endpoint{"收藏书架", accountOrigin, "/reading/bookapi/bookshelf/video/list/v", "GET", unsigned, 4 << 20}, pairs, nil, nil)

	if err != nil {
		return nil, err
	}
	return parseFavoritesResponse(body)
}

func (c *SessionClient) Likes(ctx context.Context, offset int) (*LikesResponse, error) {
	if err := c.requireAccount(); err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, ErrParamInvalid
	}
	body, err := c.call(ctx, endpoint{"点赞", accountOrigin, "/reading/ugc/action/mget/v1/", "GET", getSigned, 4 << 20}, c.devicePairs([2]string{"action_type", "3"}, [2]string{"count", "100"}, [2]string{"offset", strconv.Itoa(offset)}, [2]string{"object_type_list", "6,15,10"}), nil, nil)

	if err != nil {
		return nil, err
	}
	return parseLikesResponse(body)
}

func (c *SessionClient) Reservations(ctx context.Context, offset int, online bool) (*ReservationsResponse, error) {
	if err := c.requireAccount(); err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, ErrParamInvalid
	}
	body, err := c.call(ctx, endpoint{"预约", accountOrigin, "/reading/user/subscribe/list/v1/", "GET", getSigned, 4 << 20}, c.devicePairs([2]string{"order_experiment", "descend"}, [2]string{"offset", strconv.Itoa(offset)}, [2]string{"tab_type", "13"}, [2]string{"is_online", strconv.FormatBool(online)}), nil, nil)

	if err != nil {
		return nil, err
	}
	return parseReservationsResponse(body)
}

func (c *SessionClient) UpdateHistory(ctx context.Context, items []ProgressRecord, episodes []string, deleted bool) error {
	payload := HistoryUpdateBatchPayload(items, episodes)
	operation := "历史上报"
	if deleted {
		payload = HistoryDeletePayload(items, episodes)
		operation = "历史删除"
	}
	if payload == nil || len(payload["update_datas"].([]any)) == 0 {
		return ErrParameterInvalid
	}
	return c.mutation(ctx, operation, "/reading/bookapi/read_history/update/v", payload)
}
func (c *SessionClient) UploadProgress(ctx context.Context, items []ProgressRecord, episodes []string) error {
	payload := ProgressUploadBatchPayload(items, episodes)
	if payload == nil {
		return ErrParameterInvalid
	}
	return c.mutation(ctx, "进度上报", "/reading/bookapi/read_progress/upload/v", payload)
}
func (c *SessionClient) UpdateFavorite(ctx context.Context, series string, added bool) error {
	payload := FavoriteMutationPayload(series, added)
	if payload == nil {
		return ErrParameterInvalid
	}
	return c.mutation(ctx, "收藏同步", "/reading/bookapi/bookshelf/video/update/v", payload)
}
func (c *SessionClient) SetEpisodeLike(ctx context.Context, series, episode string, liked bool) error {
	if !validContentID(series) || !validContentID(episode) {
		return ErrParamInvalid
	}
	action := 3
	if !liked {
		action = 4
	}
	return c.mutation(ctx, "点赞", "/novel/articleapi/do_action/v1/", map[string]any{"action_category": 1, "action_reason_remark": "like_click", "action_type": action, "business_param": map[string]any{"book_id": 0, "has_aigc_content": false, "modify_count": 0, "video_id": series, "shark_param": map[string]any{"enter_from": "MainFragmentActivity", "page_list": "MainFragmentActivity", "previous_page": ""}}, "object_id": episode, "object_type": 6})
}
func (c *SessionClient) SetReservation(ctx context.Context, series string, op int) error {
	id, err := strconv.ParseInt(series, 10, 64)
	if err != nil || id <= 0 {
		return ErrParameterInvalid
	}
	return c.mutation(ctx, "预约", "/reading/bookapi/search/uncover_subscribe/v", map[string]any{"item_id": id, "item_type": 1, "op_type": op, "shark_param": map[string]any{"enter_from": "BulletActivity", "previous_page": "MainFragmentActivity", "page_list": "MainFragmentActivity,BulletActivity"}, "wish_list_all_del": 0})
}
