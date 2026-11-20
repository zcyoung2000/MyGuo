package hgapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrAccountTemporary = errors.New("红果登录服务暂不可用，请稍后重试")

type AccountProfile struct {
	UserID, Nickname, Avatar, OriginalUserID string
	LegacyRequestID                          any
}

func accountUserID(ids ...string) string {
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && id != "0" && id != "anonymous" {
			return id
		}
	}
	return ""
}
func (c *SessionClient) ValidateAccount(ctx context.Context) (*AccountProfile, error) {
	body, err := c.call(ctx, endpoint{"登录验证", accountOrigin, "/reading/user/info/v1/", "GET", unsigned, 2 << 20}, [][2]string{{"aid", "8662"}, {"app_name", "novelread"}, {"version_code", "73932"}}, nil, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.HTTPStatus < 500 && apiErr.HTTPStatus != 429 {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", ErrAccountTemporary, err)
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
	uid := accountUserID(res.Data.BizUserID, res.Data.UserID)
	if uid == "" {
		return nil, fmt.Errorf("登录验证未通过: 凭据未返回有效账号")
	}
	nickname := strings.TrimSpace(res.Data.UserName)
	if nickname == "" {
		nickname = "myguo用户"
	}
	avatar := strings.TrimSpace(res.Data.AvatarURL)

	return &AccountProfile{UserID: uid, Nickname: nickname, Avatar: avatar, OriginalUserID: res.Data.UserID, LegacyRequestID: res.Data.ReqID}, nil
}
