package ipc

import (
	"context"

	"myguo/internal/hgapi"
)

type Danmaku = hgapi.Danmaku
type DanmakuPage = hgapi.DanmakuPage
type DanmakuSendResult = hgapi.DanmakuSendResult

func (s *HongguoService) GetDanmaku(ctx context.Context, series, episode string, start, duration int64) (*DanmakuPage, error) {
	api, err := s.requestAPI(ctx)
	if err != nil {
		return nil, err
	}
	page, err := api.GetDanmaku(ctx, series, episode, start, duration)
	return sanitizeIPC(page), err
}
func (s *HongguoService) SendDanmaku(ctx context.Context, series, episode, text string, at int64, reply string) (*DanmakuSendResult, error) {
	api, err := s.requestAPI(ctx)
	if err != nil {
		return nil, err
	}
	result, err := api.SendDanmaku(ctx, series, episode, text, at, reply)
	if err != nil {
		return nil, err
	}
	result.UserID = s.store.GetProfile().UserID
	return sanitizeIPC(result), nil
}
func (s *HongguoService) LikeDanmaku(ctx context.Context, episode, id string, liked bool) (bool, error) {
	api, err := s.requestAPI(ctx)
	if err != nil {
		return false, err
	}
	return api.LikeDanmaku(ctx, episode, id, liked)
}
