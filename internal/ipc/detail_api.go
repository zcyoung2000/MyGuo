package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"myguo/internal/sign"
)

// detailRequest reuses the application's current SDK identity, account and
// upstream HTTP transport. These two detail surfaces are not exposed by the
// pinned SDK. Sign the exact request bytes, without rewriting an SDK request.
func (s *HongguoService) detailRequest(ctx context.Context, method, origin, path string, extra [][2]string, body []byte, headers map[string]string) ([]byte, error) {
	device, iid, cdid, openudid := s.deviceIdentity()
	cookies, token := s.authCredentials()
	if cookies == "" && s.deviceProfileStore != nil {
		profile, err := s.deviceProfileStore.Load(ctx)
		if err != nil {
			return nil, err
		}
		if profile != nil && profile.DeviceID != "" && profile.InstallID != "" {
			device, iid, cdid, openudid = profile.DeviceID, profile.InstallID, profile.CDID, profile.OpenUDID
			cookies = "passport_csrf_token=; passport_csrf_token_default=; store-region=" + profile.StoreRegion + "; store-region-src=" + profile.StoreRegionSrc + "; install_id=" + profile.InstallID + "; ttreq=" + profile.TTReq
		}
	}
	pairs := append(defaultDeviceQueryPairs(device, iid, cdid, openudid), extra...)
	query, signed := sign.SignPOST(path, pairs, time.Now().UnixMilli(), device, "7.3.9.32", body, nil)
	req, err := http.NewRequestWithContext(ctx, method, origin+path+"?"+query, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range signed {
		req.Header.Set(key, value)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	ua, region := s.runtimeIdentity()
	req.Header.Set("User-Agent", ua)
	if cookies != "" {
		req.Header.Set("Cookie", cookies)
	}
	if token != "" {
		req.Header.Set("x-tt-token", token)
	}
	if region != "" {
		req.Header.Set("x-tt-store-region", region)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("详情接口返回 HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 4<<20 {
		return nil, fmt.Errorf("详情响应过大")
	}
	return raw, nil
}

type officialRelatedRow struct {
	ID          string `json:"series_id"`
	Title       string `json:"title"`
	Cover       string `json:"cover"`
	Description string `json:"abstract"`
	Episodes    int    `json:"episode_cnt"`
	ContentType int    `json:"content_type"`
	RightText   string `json:"right_text"`
	Tag         struct {
		Text string `json:"text"`
	} `json:"tag_info"`
}

func adaptationOffset(seriesID, cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	parts := strings.Split(cursor, ":")
	if len(parts) != 3 || parts[0] != "related1" || parts[1] != seriesID {
		return 0, fmt.Errorf("改编作品游标不属于当前短剧")
	}
	offset, err := strconv.Atoi(parts[2])
	if err != nil || offset <= 0 || offset > 1000000 {
		return 0, fmt.Errorf("改编作品游标无效")
	}
	return offset, nil
}

func (s *HongguoService) officialAdaptations(ctx context.Context, seriesID, cursor string, exclude map[string]bool) (*SeriesCollectionDTO, error) {
	offset, err := adaptationOffset(seriesID, cursor)
	if err != nil {
		return nil, err
	}
	pairs := [][2]string{{"book_id", seriesID}, {"filter_ids", seriesID}, {"from", "detail_page_more_related"}, {"scene", "10"}, {"offset", strconv.Itoa(offset)}, {"session_uuid", uuid.NewString()}}
	raw, err := s.detailRequest(ctx, http.MethodGet, "https://api3-normal-lf.fqnovel.com", "/reading/bookapi/plan/v1/", pairs, nil, nil)
	if err != nil {
		return nil, err
	}
	var env struct {
		Code int `json:"code"`
		Data []struct {
			ShowType   int                  `json:"show_type"`
			Items      []officialRelatedRow `json:"video_data"`
			HasMore    bool                 `json:"has_more"`
			NextOffset int                  `json:"next_offset"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("相关作品响应解析失败: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("相关作品接口返回错误 %d", env.Code)
	}
	out := &SeriesCollectionDTO{Title: "同一剧本改编", Items: []SeriesCard{}, Seasons: []SeriesCard{}}
	seen := map[string]bool{}
	for _, cell := range env.Data {
		// In this scene 547 contains both relations. Partition by the explicit
		// relation tag, not the title or season_index (other versions have seasons
		// too). Never accept guess cells.
		if cell.ShowType != 547 {
			continue
		}
		if cell.HasMore {
			if cell.NextOffset <= offset {
				return nil, fmt.Errorf("相关作品分页没有前进")
			}
			out.HasMore = true
			out.Cursor = fmt.Sprintf("related1:%s:%d", seriesID, cell.NextOffset)
		}
		for _, item := range cell.Items {
			tag := strings.TrimSpace(item.Tag.Text)
			season := strings.HasPrefix(tag, "第") && strings.HasSuffix(tag, "季") && len([]rune(tag)) > 2
			if (!season && tag != "同IP") || !validContentID(item.ID) || item.ID == seriesID || seen[item.ID] || (!season && exclude[item.ID]) {
				continue
			}
			seen[item.ID] = true
			upcoming := isUpcomingSeries(item.Episodes, item.RightText)
			status, heat := "", ""
			if upcoming {
				status, heat = "upcoming", "待上线"
			}
			card := SeriesCard{ID: item.ID, Title: item.Title, Description: item.Description, CoverURL: s.media.ImageURL(item.Cover), EpisodeCount: item.Episodes, ContentType: item.ContentType, Category: seriesContentTypeLabel(item.ContentType), Upcoming: upcoming, Status: status, Heat: heat, Reserved: s.store.HasReservation(item.ID)}
			if season {
				card.BadgeText = tag
				out.Seasons = append(out.Seasons, card)
			} else {
				out.Items = append(out.Items, card)
			}
		}
	}
	return sanitizeIPC(out), nil
}

type ReviewDTO struct {
	CommentDTO
	Score     float64 `json:"score,omitempty"`
	ScoreNote string  `json:"scoreNote,omitempty"`
}

type ReviewPageDTO struct {
	Items      []ReviewDTO `json:"items"`
	Total      int64       `json:"total"`
	NextCursor string      `json:"nextCursor,omitempty"`
	HasMore    bool        `json:"hasMore"`
	Score      float64     `json:"score,omitempty"`
	ScoreCount int64       `json:"scoreCount,omitempty"`
}

func apiNumber(raw json.RawMessage) float64 {
	text := strings.Trim(string(raw), "\"")
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1e10 {
		return 0
	}
	return n
}

func parseReviewPage(raw []byte, seriesID, cursor string) (*ReviewPageDTO, error) {
	page, err := parseCommentPage(raw, seriesID, seriesID, cursor)
	if err != nil {
		return nil, err
	}
	if page.HasMore && (page.NextCursor == "" || validCommentCursor(page.NextCursor) != nil) {
		return nil, fmt.Errorf("剧评分页游标无效")
	}
	var env struct {
		Data struct {
			Items []struct {
				Comment struct {
					ID     string `json:"comment_id"`
					Common struct {
						GroupID string `json:"group_id"`
						Status  int    `json:"status"`
						Content struct {
							Text string `json:"text"`
						} `json:"content"`
						User struct {
							Base struct {
								Avatar string `json:"expand_user_avatar"`
							} `json:"base_info"`
						} `json:"user_info"`
					} `json:"common"`
					Expand struct {
						BookID string          `json:"book_id"`
						Score  json.RawMessage `json:"score"`
						Note   string          `json:"score_suffix_text"`
					} `json:"expand"`
				} `json:"comment"`
			} `json:"data_list"`
			Extra struct {
				Score json.RawMessage `json:"credibility_score"`
				Count json.RawMessage `json:"credibility_score_count"`
			} `json:"extra"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	byID := make(map[string]int, len(env.Data.Items))
	for i, row := range env.Data.Items {
		// Match the first valid record selected by parseCommentPage; a duplicate
		// from another group must not replace its score or avatar.
		wire := row.Comment
		if _, exists := byID[wire.ID]; exists {
			continue
		}
		content := strings.TrimSpace(wire.Common.Content.Text)
		if wire.Common.Status != 1 || wire.Common.GroupID != seriesID ||
			(wire.Expand.BookID != "" && wire.Expand.BookID != seriesID) || content == "" || len(content) > 20000 {
			continue
		}
		byID[row.Comment.ID] = i
	}
	out := &ReviewPageDTO{Items: []ReviewDTO{}, Total: page.Total, HasMore: page.HasMore, NextCursor: page.NextCursor}
	for _, item := range page.Items {
		wire := env.Data.Items[byID[item.ID]].Comment
		if wire.Common.User.Base.Avatar != "" {
			item.AvatarURL = wire.Common.User.Base.Avatar
		}
		score := apiNumber(wire.Expand.Score)
		if score > 10 {
			score = 0
		}
		out.Items = append(out.Items, ReviewDTO{CommentDTO: item, Score: score, ScoreNote: wire.Expand.Note})
	}
	if n := apiNumber(env.Data.Extra.Count); n > 0 {
		out.ScoreCount = int64(n)
		out.Score = apiNumber(env.Data.Extra.Score)
		if out.Score > 10 {
			out.Score, out.ScoreCount = 0, 0
		}
	}
	return out, nil
}

// GetSeriesReviews reads series-level reviews (group_type=1), not episode
// comments (group_type=30). Parameters recovered from the shipped client and
// verified on the live endpoint, including its JSON continuation cursor.
func (s *HongguoService) GetSeriesReviews(ctx context.Context, seriesID, cursor string) (*ReviewPageDTO, error) {
	if !validContentID(seriesID) {
		return nil, fmt.Errorf("无效短剧 ID")
	}
	if err := validCommentCursor(cursor); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	body := map[string]any{"comment_source": 1, "server_channel": 34, "group_id": seriesID, "group_type": 1, "comment_type": 2, "sort": 1, "count": 10, "business_param": map[string]any{"book_id": seriesID, "req_type": 0, "need_count": true}}
	if cursor != "" {
		body["cursor"] = cursor
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	raw, err := s.detailRequest(ctx, http.MethodPost, commentAPIOrigin, "/novel/commentapi/comment/list/"+seriesID+"/v1/", nil, payload, map[string]string{"Comment-Source": "1", "Server-Channel": "34"})
	if err != nil {
		return nil, err
	}
	page, err := parseReviewPage(raw, seriesID, cursor)
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		page.Items[i].AvatarURL = s.media.ImageURL(page.Items[i].AvatarURL)
	}
	return sanitizeIPC(page), nil
}

func (s *HongguoService) GetSeriesReviewReplies(ctx context.Context, seriesID, reviewID, cursor string) (*CommentPageDTO, error) {
	if !validContentID(seriesID) || !validContentID(reviewID) {
		return nil, fmt.Errorf("无效剧评 ID")
	}
	if err := validReplyCursor(cursor); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	body := map[string]any{"comment_source": 501, "server_channel": 34, "group_id": seriesID, "group_type": 1, "comment_id": reviewID, "count": 10, "comment_type": 2, "business_param": map[string]any{"book_id": seriesID, "req_type": 0, "real_level": 2, "need_count": true}}
	if cursor != "" {
		body["cursor"] = cursor
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	raw, err := s.detailRequest(ctx, http.MethodPost, commentAPIOrigin, "/novel/commentapi/reply/list/"+reviewID+"/v1/", nil, payload, map[string]string{"Comment-Source": "501", "Server-Channel": "34"})
	if err != nil {
		return nil, err
	}
	page, err := parseReplyPage(raw, seriesID, seriesID, reviewID, cursor)
	if err != nil {
		return nil, err
	}
	if page.HasMore && (page.NextCursor == "" || validReplyCursor(page.NextCursor) != nil) {
		return nil, fmt.Errorf("剧评回复游标无效")
	}
	for i := range page.Items {
		page.Items[i].AvatarURL = s.media.ImageURL(page.Items[i].AvatarURL)
	}
	return sanitizeIPC(page), nil
}
