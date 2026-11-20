package library

import (
	"context"
	"fmt"
	"strconv"

	"myguo/internal/hgapi"
)

func (s *Service) apiForSession(session syncSession) *hgapi.SessionClient {
	device, iid, cdid, openudid := s.deviceIdentity()
	ua, region := s.runtimeInfo()
	return hgapi.NewSessionClient(s.httpClient, hgapi.Session{DeviceID: device, InstallID: iid, CDID: cdid, OpenUDID: openudid, Cookies: session.cookies, Token: session.token, UserAgent: ua, StoreRegion: region})
}

func (s *Service) updateHistory(ctx context.Context, items []HistoryItem, episodes []string, deleted bool) error {
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return err
	}
	return s.apiForSession(session).UpdateHistory(ctx, apiProgressBatch(items), episodes, deleted)
}
func (s *Service) uploadProgress(ctx context.Context, items []HistoryItem, episodes []string) error {
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return err
	}
	return s.apiForSession(session).UploadProgress(ctx, apiProgressBatch(items), episodes)
}
func (s *Service) updateFavorite(ctx context.Context, series string, added bool) error {
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return err
	}
	return s.apiForSession(session).UpdateFavorite(ctx, series, added)
}

func contentInt(s string) (int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("内容 ID 无效")
	}
	return v, nil
}
