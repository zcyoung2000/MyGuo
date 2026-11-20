package ipc

import (
	"encoding/json"

	"myguo/internal/hgapi"
)

func apiNumber(raw json.RawMessage) float64 { return hgapi.ReviewNumber(raw) }
func parseReviewPage(raw []byte, series, cursor string) (*ReviewPageDTO, error) {
	return hgapi.ParseReviewPage(raw, series, cursor)
}
func parseCommentPage(raw []byte, series, episode, cursor string) (*CommentPageDTO, error) {
	return hgapi.ParseCommentPage(raw, series, episode, cursor)
}
func parseReplyPage(raw []byte, series, episode, comment, cursor string) (*CommentPageDTO, error) {
	return hgapi.ParseReplyPage(raw, series, episode, comment, cursor)
}
func parseDanmakuPage(raw []byte, series, episode string, start, duration int64) (*DanmakuPage, error) {
	return hgapi.ParseDanmakuPage(raw, series, episode, start, duration)
}
