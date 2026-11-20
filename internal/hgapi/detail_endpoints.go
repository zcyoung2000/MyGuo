package hgapi

import (
	"context"
	"strconv"

	googleuuid "github.com/google/uuid"
)

func (c *SessionClient) RelatedWorks(ctx context.Context, series string, offset int) ([]RelatedWorkCell, error) {
	if !validContentID(series) || offset < 0 || offset > 1_000_000 {
		return nil, ErrParamInvalid
	}
	raw, err := c.call(ctx, endpoint{"相关作品", relatedOrigin, "/reading/bookapi/plan/v1/", "GET", postSigned, 4 << 20}, c.devicePairs([2]string{"book_id", series}, [2]string{"filter_ids", series}, [2]string{"from", "detail_page_more_related"}, [2]string{"scene", "10"}, [2]string{"offset", strconv.Itoa(offset)}, [2]string{"session_uuid", googleuuid.NewString()}), nil, nil)

	if err != nil {
		return nil, err
	}
	return parseRelatedWorks(raw)
}

func (c *SessionClient) SeriesReviews(ctx context.Context, series, cursor string) (*ReviewPageDTO, error) {
	if !validContentID(series) {
		return nil, ErrParamInvalid
	}
	if err := ValidCommentCursor(cursor); err != nil {
		return nil, err
	}
	payload := map[string]any{"comment_source": 1, "server_channel": 34, "group_id": series, "group_type": 1, "comment_type": 2, "sort": 1, "count": 10, "business_param": map[string]any{"book_id": series, "req_type": 0, "need_count": true}}
	if cursor != "" {
		payload["cursor"] = cursor
	}
	raw, err := c.comment(ctx, "/novel/commentapi/comment/list/"+series+"/v1/", "1", "34", payload, 4<<20)

	if err != nil {
		return nil, err
	}
	return ParseReviewPage(raw, series, cursor)
}

func (c *SessionClient) SeriesReviewReplies(ctx context.Context, series, review, cursor string) (*CommentPageDTO, error) {
	if !validContentID(series) || !validContentID(review) {
		return nil, ErrParamInvalid
	}
	if err := ValidReplyCursor(cursor); err != nil {
		return nil, err
	}
	payload := map[string]any{"comment_source": 501, "server_channel": 34, "group_id": series, "group_type": 1, "comment_id": review, "count": 10, "comment_type": 2, "business_param": map[string]any{"book_id": series, "req_type": 0, "real_level": 2, "need_count": true}}
	if cursor != "" {
		payload["cursor"] = cursor
	}
	raw, err := c.comment(ctx, "/novel/commentapi/reply/list/"+review+"/v1/", "501", "34", payload, 4<<20)

	if err != nil {
		return nil, err
	}
	return ParseReplyPage(raw, series, series, review, cursor)
}
