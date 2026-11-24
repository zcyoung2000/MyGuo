package ipc

import (
	"context"

	"myguo/internal/hgapi"
)

type Danmaku = hgapi.Danmaku
type DanmakuPage = hgapi.DanmakuPage

func (s *HongguoService) GetDanmaku(ctx context.Context, series, episode string, start, duration int64) (*DanmakuPage, error) {
	api, err := s.requestAPI(ctx)
	if err != nil {
		return nil, err
	}
	page, err := api.GetDanmaku(ctx, series, episode, start, duration)
	return sanitizeIPC(page), err
}
func (s *HongguoService) LikeDanmaku(ctx context.Context, episode, id string, liked bool) (bool, error) {
	api, err := s.requestAPI(ctx)
	if err != nil {
		return false, err
	}
	return api.LikeDanmaku(ctx, episode, id, liked)
}
