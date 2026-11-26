package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/passport"
	"myguo/internal/native"
	"myguo/internal/store"
)

func (s *HongguoService) ResetDevice(ctx context.Context) error {
	return s.resetDeviceWithContext(ctx)
}

func (s *HongguoService) resetDeviceWithContext(ctx context.Context) error {
	if s.resetDevice != nil {
		if err := s.resetDevice(ctx); err != nil {
			return err
		}
	} else if s.client != nil {
		if err := s.client.ResetDevice(ctx); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("device reset is unavailable")
	}
	if s.deviceProfileStore != nil {
		if err := s.reloadDeviceIdentity(); err != nil {
			return err
		}
	}
	if s.store != nil && s.media != nil {
		s.invalidateUserDataCaches()
	}
	return nil
}

func (s *HongguoService) ClearCache(ctx context.Context) (int64, error) {
	s.invalidateUserDataCaches()
	cleared, err := s.media.ClearCache()
	if clearErr := s.store.PruneCachedEpisodes(func(item store.CachedEpisode) bool {
		return s.media.HasCachedEpisode(item.EpisodeID, item.Quality)
	}); err == nil {
		err = clearErr
	}
	return cleared, err
}

func (s *HongguoService) GetCacheSize(ctx context.Context) (int64, error) {
	return s.media.GetCacheSize(), nil
}

func (s *HongguoService) GetDeviceInfo(ctx context.Context) (map[string]string, error) {
	deviceID, iid, cdid, openudid := s.deviceIdentity()
	_, storeRegion := s.runtimeIdentity()
	return map[string]string{
		"deviceId":    deviceID,
		"installId":   iid,
		"cdid":        cdid,
		"openudid":    openudid,
		"storeRegion": storeRegion,
		"version":     "2.0.0",
		"arch":        runtime.GOOS + "-" + runtime.GOARCH,
	}, nil
}

func (s *HongguoService) LoginWithCookies(ctx context.Context, cookies, token string) (*UserProfile, error) {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	return s.loginWithCookiesLocked(ctx, cookies, token)
}

func (s *HongguoService) loginWithCookiesLocked(ctx context.Context, cookies, token string) (*UserProfile, error) {
	previousUserID, previousCookies, _ := s.authSession()
	cookies = strings.TrimSpace(cookies)
	token = strings.TrimSpace(token)

	// Support parsing raw JSON input if the user pasted a JSON string
	if strings.HasPrefix(cookies, "{") && strings.HasSuffix(cookies, "}") {
		var rawObj struct {
			Cookies string `json:"cookies"`
			Token   string `json:"token"`
		}
		if err := json.Unmarshal([]byte(cookies), &rawObj); err == nil && rawObj.Cookies != "" {
			cookies = rawObj.Cookies
			if token == "" && rawObj.Token != "" {
				token = rawObj.Token
			}
		}
	}

	if cookies == "" {
		return nil, fmt.Errorf("Cookie 不能为空")
	}

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	api, err := s.apiForCredentials(ctx, cookies, token)
	if err != nil {
		return nil, err
	}
	account, err := api.ValidateAccount(ctx)
	if err != nil {
		if errors.Is(err, hgapi.ErrAccountTemporary) {
			return nil, fmt.Errorf("%w: %w", passport.ErrTemporary, err)
		}
		return nil, err
	}
	uid, nickname, avatar := account.UserID, account.Nickname, account.Avatar

	profile := UserProfile{
		UserID: uid, Nickname: nickname, Avatar: avatar, LoggedIn: true,
		DeviceID: s.store.GetProfile().DeviceID,
	}
	// A profile refresh must not masquerade as an account switch. Older builds
	// persisted req_id as the user ID; accept it only as a migration alias tied
	// to the exact existing credential, never as evidence of login by itself.
	sameAccount := previousUserID != "" && (previousUserID == uid || previousUserID == nonZeroUserID(account.OriginalUserID))
	var aliases []string
	legacyID := legacyAccountID(account.LegacyRequestID)
	if previousCookies != "" && cookies == previousCookies && legacyID != "" && previousUserID == legacyID {
		sameAccount = true
		aliases = append(aliases, "user:"+legacyID)
	}
	// Once the profile is canonical, req_id alone cannot prove ownership of an
	// orphaned queue. Only recover the canonical account's own pending records.
	if sameAccount && previousUserID != "" {
		aliases = append(aliases, "user:"+previousUserID)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.store.SetAuthenticatedAccount(store.AccountAuth{
		Cookies: cookies,
		Token:   token,
	}, profile, !sameAccount, aliases...); err != nil {
		return nil, fmt.Errorf("保存登录账号失败: %w", err)
	}
	s.setAuthCredentials(cookies, token, uid)

	// Invalidate user data caches
	s.invalidateUserDataCaches()

	if profile.Avatar != "" {
		profile.Avatar = s.media.ImageURL(profile.Avatar)
	}
	return sanitizeIPC(&profile), nil
}

func legacyAccountID(value any) string {
	var id string
	switch v := value.(type) {
	case string:
		id = v
	case float64:
		id = strconv.FormatFloat(v, 'f', -1, 64)
	}
	if !validContentID(id) {
		return ""
	}
	return id
}

func nonZeroUserID(value string) string {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return ""
	}
	return strings.TrimSpace(value)
}

func (s *HongguoService) RefreshUserProfile(ctx context.Context) (*UserProfile, error) {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	userCookies, userToken := s.authCredentials()
	if userCookies == "" {
		return nil, fmt.Errorf("当前未登录")
	}
	return s.loginWithCookiesLocked(ctx, userCookies, userToken)
}

func (s *HongguoService) Logout(ctx context.Context) error {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	s.qrMu.Lock()
	if s.qrActive != nil {
		s.qrActive.cancel()
		s.qrActive = nil
	}
	s.qrMu.Unlock()
	deviceID := s.store.GetProfile().DeviceID
	if err := s.store.SignOut(deviceID); err != nil {
		return fmt.Errorf("退出登录失败: %w", err)
	}
	s.setAuthCredentials("", "")

	s.cache.Reset()
	s.invalidateUserDataCaches()

	return nil
}

// ============================================================================
// macOS Native Integration IPC Handlers
// ============================================================================

func (s *HongguoService) UpdateNowPlaying(ctx context.Context, args UpdateNowPlayingArgs) (bool, error) {
	cacheDir := ""
	if s.media != nil {
		cacheDir = s.media.CacheDir()
	}
	native.UpdateNowPlaying(cacheDir, args)
	return true, nil
}

func (s *HongguoService) ClearNowPlaying(ctx context.Context) (bool, error) {
	native.ClearNowPlaying()
	return true, nil
}

func (s *HongguoService) SetPlaybackKeepAwake(ctx context.Context, active bool) (bool, error) {
	native.SetPlaybackKeepAwake(active)
	return true, nil
}

func (s *HongguoService) ShowConfirmDialog(ctx context.Context, args ConfirmDialogArgs) (bool, error) {
	return native.ShowConfirmDialog(args)
}

func (s *HongguoService) SendNativeNotification(ctx context.Context, args NotificationArgs) (bool, error) {
	return native.SendNotification(args)
}

func (s *HongguoService) TogglePictureInPicture(ctx context.Context) (bool, error) {
	native.TogglePictureInPicture()
	return true, nil
}
