package hgapi

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

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

func ReviewNumber(raw json.RawMessage) float64 {
	text := strings.Trim(string(raw), "\"")
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1e10 {
		return 0
	}
	return n
}

func ParseReviewPage(raw []byte, seriesID, cursor string) (*ReviewPageDTO, error) {
	page, err := ParseCommentPage(raw, seriesID, seriesID, cursor)
	if err != nil {
		return nil, err
	}
	if page.HasMore && (page.NextCursor == "" || ValidCommentCursor(page.NextCursor) != nil) {
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
		score := ReviewNumber(wire.Expand.Score)
		if score > 10 {
			score = 0
		}
		out.Items = append(out.Items, ReviewDTO{CommentDTO: item, Score: score, ScoreNote: wire.Expand.Note})
	}
	if n := ReviewNumber(env.Data.Extra.Count); n > 0 {
		out.ScoreCount = int64(n)
		out.Score = ReviewNumber(env.Data.Extra.Score)
		if out.Score > 10 {
			out.Score, out.ScoreCount = 0, 0
		}
	}
	return out, nil
}

// GetSeriesReviews reads series-level reviews (group_type=1), not episode
// comments (group_type=30). Parameters recovered from the shipped client and
// verified on the live endpoint, including its JSON continuation cursor.
