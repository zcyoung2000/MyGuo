package ipc

import (
	"context"

	"myguo/internal/hgapi"
)

// requestAPI binds interaction calls to the account that owns the scene.
// Identity lookup is composition here; HTTP and wire policy live in hgapi.
func (s *HongguoService) requestAPI(ctx context.Context) (*hgapi.SessionClient, error) {
	cookies, token, err := s.libraryService().PlaybackCredentials(ctx)
	if err != nil {
		return nil, err
	}
	return s.apiForCredentials(ctx, cookies, token)
}

func (s *HongguoService) apiForCredentials(ctx context.Context, cookies, token string) (*hgapi.SessionClient, error) {
	device, iid, cdid, openudid := s.deviceIdentity()
	guest := cookies == ""
	if cookies == "" && s.deviceProfileStore != nil {
		profile, err := s.deviceProfileStore.Load(ctx)
		if err != nil {
			return nil, err
		}
		if profile != nil && profile.DeviceID != "" && profile.InstallID != "" {
			device, iid, cdid, openudid = profile.DeviceID, profile.InstallID, profile.CDID, profile.OpenUDID
			cookies = hgapi.GuestCookies(profile)
		}
	}
	ua, region := s.runtimeIdentity()
	return hgapi.NewSessionClient(s.httpClient, hgapi.Session{DeviceID: device, InstallID: iid, CDID: cdid, OpenUDID: openudid, Cookies: cookies, Token: token, UserAgent: ua, StoreRegion: region, Guest: guest}), nil
}
