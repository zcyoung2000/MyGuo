package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"myguo/internal/native"
	"myguo/internal/qrauth"
	"myguo/internal/store"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
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

	reqURL := "https://api5-normal-sinfonlinea.fqnovel.com/reading/user/info/v1/?aid=8662&app_name=novelread&version_code=73932"
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建验证请求失败: %w", err)
	}
	userAgent, storeRegion := s.runtimeIdentity()

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cookie", cookies)
	if token != "" {
		req.Header.Set("x-tt-token", token)
	}
	req.Header.Set("x-tt-store-region", storeRegion)
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, qrauth.ErrTemporary
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w（HTTP %d）", qrauth.ErrTemporary, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取接口响应失败: %w", err)
	}

	var res struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			UserName  string `json:"user_name"`
			AvatarURL string `json:"avatar_url"`
			UserID    string `json:"user_id"`
			BizUserID string `json:"biz_user_id"`
			ReqID     any    `json:"req_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("解析用户信息响应失败: %w", err)
	}

	if res.Code != 0 {
		msg := res.Message
		if msg == "" {
			msg = "凭据无效或已失效"
		}
		return nil, fmt.Errorf("登录验证未通过: %s (code: %d)", msg, res.Code)
	}

	// Anonymous /user/info responses also return code 0. req_id is not an
	// account ID, so only a non-zero account or business account proves login.
	uid := firstNonEmpty(
		nonZeroUserID(res.Data.BizUserID),
		nonZeroUserID(res.Data.UserID),
	)
	if uid == "" {
		return nil, fmt.Errorf("登录验证未通过: 凭据未返回有效账号")
	}
	nickname := strings.TrimSpace(res.Data.UserName)
	if nickname == "" {
		nickname = "myguo用户"
	}
	avatar := strings.TrimSpace(res.Data.AvatarURL)

	profile := UserProfile{
		UserID: uid, Nickname: nickname, Avatar: avatar, LoggedIn: true,
		DeviceID: s.store.GetProfile().DeviceID,
	}
	// A profile refresh must not masquerade as an account switch. Older builds
	// persisted req_id as the user ID; accept it only as a migration alias tied
	// to the exact existing credential, never as evidence of login by itself.
	sameAccount := previousUserID != "" && (previousUserID == uid || previousUserID == nonZeroUserID(res.Data.UserID))
	var aliases []string
	legacyID := legacyAccountID(res.Data.ReqID)
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

func (s *HongguoService) CheckExistingAccount(ctx context.Context) (map[string]any, error) {
	home, _ := os.UserHomeDir()
	oldAccPath := filepath.Join(home, "Library", "Application Support", "hgplayer", "account.json")
	data, err := os.ReadFile(oldAccPath)
	if err != nil {
		return sanitizeIPC(map[string]any{"exists": false}), nil
	}
	var acc struct {
		Cookies string `json:"cookies"`
		Token   string `json:"token"`
		Profile struct {
			UserID   string `json:"userId"`
			Nickname string `json:"nickname"`
			Avatar   string `json:"avatar"`
			LoggedIn bool   `json:"loggedIn"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &acc); err != nil || acc.Cookies == "" {
		return sanitizeIPC(map[string]any{"exists": false}), nil
	}

	avatar := acc.Profile.Avatar
	if avatar != "" {
		avatar = s.media.ImageURL(avatar)
	}

	return sanitizeIPC(map[string]any{
		"exists":   true,
		"nickname": acc.Profile.Nickname,
		"userId":   acc.Profile.UserID,
		"avatar":   avatar,
	}), nil
}

func (s *HongguoService) ImportHgplayerAccount(ctx context.Context) (*UserProfile, error) {
	home, _ := os.UserHomeDir()
	oldAccPath := filepath.Join(home, "Library", "Application Support", "hgplayer", "account.json")
	data, err := os.ReadFile(oldAccPath)
	if err != nil {
		return nil, fmt.Errorf("未找到旧版应用账号数据")
	}
	var acc struct {
		Cookies string `json:"cookies"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(data, &acc); err != nil || acc.Cookies == "" {
		return nil, fmt.Errorf("旧版账号数据无效")
	}
	return s.LoginWithCookies(ctx, acc.Cookies, acc.Token)
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
