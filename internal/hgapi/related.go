package hgapi

import (
	"encoding/json"
	"fmt"
)

type RelatedWorkRow struct {
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
type RelatedWorkCell struct {
	ShowType   int              `json:"show_type"`
	Items      []RelatedWorkRow `json:"video_data"`
	HasMore    bool             `json:"has_more"`
	NextOffset int              `json:"next_offset"`
}

func parseRelatedWorks(raw []byte) ([]RelatedWorkCell, error) {
	var env struct {
		Code int               `json:"code"`
		Data []RelatedWorkCell `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("相关作品响应解析失败: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("相关作品接口返回错误 %d", env.Code)
	}
	return env.Data, nil
}
