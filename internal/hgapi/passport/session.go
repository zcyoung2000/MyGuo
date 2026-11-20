// Package passport implements the official website's QR authorization protocol.
// Each attempt owns a fresh, in-memory cookie jar. Account credentials are
// available only after phone confirmation and a successful Passport check.
package passport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"myguo/internal/hgapi"
)

const Origin = "https://hongguoduanju.com"
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

var ErrExpired = errors.New("二维码已过期，请刷新二维码")
var ErrRejected = errors.New("已在手机上取消登录")
var ErrTemporary = errors.New("红果登录服务暂不可用，请稍后重试")

type Session struct {
	client    *http.Client
	token     string
	Image     []byte
	ExpiresAt time.Time
	confirmed bool
	redirect  string
}

type Credentials struct{ Cookies, Token string }

func New(ctx context.Context, transport http.RoundTripper) (*Session, error) {
	jar, _ := cookiejar.New(nil)
	s := &Session{client: &http.Client{Transport: transport, Jar: jar, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	data, err := s.call(ctx, "/passport/web/get_qrcode/", url.Values{"next": {Origin + "/"}})
	if err != nil {
		return nil, err
	}
	s.token = stringValue(data["token"])
	encoded := stringValue(data["qrcode"])
	if _, v, ok := strings.Cut(encoded, "base64,"); ok {
		encoded = v
	}
	s.Image, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil || s.token == "" || len(s.Image) == 0 || len(s.Image) > 1<<20 {
		return nil, errors.New("官网未返回有效二维码，请重试")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(s.Image))
	if err != nil || format != "png" || cfg.Width < 16 || cfg.Height < 16 || cfg.Width > 2048 || cfg.Height > 2048 {
		return nil, errors.New("官网二维码图片无效")
	}
	expiry, _ := strconv.ParseInt(stringValue(data["expire_time"]), 10, 64)
	now := time.Now()
	if expiry > 1_000_000_000 {
		s.ExpiresAt = time.Unix(expiry, 0)
	} else {
		s.ExpiresAt = now.Add(time.Duration(expiry) * time.Second)
	}
	if !s.ExpiresAt.After(now) {
		return nil, ErrExpired
	}
	if s.ExpiresAt.After(now.Add(10 * time.Minute)) {
		s.ExpiresAt = now.Add(10 * time.Minute)
	}
	return s, nil
}

// Poll returns waiting/scanned until both phone confirmation and Passport
// authentication succeed. It must be called serially by the owning attempt.
func (s *Session) Poll(ctx context.Context) (string, *Credentials, error) {
	if s.confirmed {
		return s.complete(ctx)
	}
	if !time.Now().Before(s.ExpiresAt.Add(5 * time.Second)) {
		return "expired", nil, ErrExpired
	}
	data, err := s.call(ctx, "/passport/web/check_qrconnect/", url.Values{"next": {Origin + "/"}, "token": {s.token}})
	if err != nil {
		return "", nil, err
	}
	switch stringValue(data["status"]) {
	case "0", "1", "new", "pending", "unscanned":
		return "waiting", nil, nil
	case "2", "scanned":
		return "scanned", nil, nil
	case "4", "cancelled", "canceled", "rejected":
		return "rejected", nil, ErrRejected
	case "5", "expired":
		return "expired", nil, ErrExpired
	case "3", "confirmed", "success":
		s.confirmed = true
		s.redirect = stringValue(data["redirect_url"])
	default:
		return "", nil, errors.New("官网扫码状态异常，请刷新二维码")
	}
	return s.complete(ctx)
}

func (s *Session) PhoneConfirmed() bool { return s.confirmed }

func (s *Session) complete(ctx context.Context) (string, *Credentials, error) {
	if s.redirect != "" {
		if err := s.finishRedirect(ctx, s.redirect); err != nil {
			return "", nil, err
		}
		s.redirect = ""
	}
	endpoint, _ := url.Parse(Origin + "/")
	hasSession := false
	for _, c := range s.client.Jar.Cookies(endpoint) {
		if (c.Name == "sessionid" || c.Name == "sessionid_ss") && c.Value != "" {
			hasSession = true
		}
	}
	if !hasSession {
		return "", nil, errors.New("手机确认后官网未返回有效会话，请重新扫码")
	}
	if _, err := s.call(ctx, "/passport/account/info/v2/", nil); err != nil {
		return "", nil, err
	}
	var cookies []string
	for _, c := range s.client.Jar.Cookies(endpoint) {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	return "confirmed", &Credentials{Cookies: strings.Join(cookies, "; ")}, nil
}

func (s *Session) request(ctx context.Context, path string, params url.Values) (*hgapi.Response, error) {
	u, _ := url.Parse(Origin + path)
	q := u.Query()
	for k, v := range map[string]string{"aid": "513755", "passport_jssdk_version": "3.4.10", "passport_jssdk_type": "normal", "account_sdk_source": "web", "is_from_ttaccountsdk": "1", "language": "zh"} {
		q.Set(k, v)
	}
	for k, v := range params {
		q[k] = v
	}
	u.RawQuery = q.Encode()
	headers := map[string]string{"User-Agent": userAgent, "Origin": Origin, "Referer": Origin + "/", "Accept": "application/json, text/javascript"}
	for _, cookie := range s.client.Jar.Cookies(u) {
		if cookie.Name == "passport_csrf_token" {
			headers["x-tt-passport-csrf-token"] = cookie.Value
		}
	}
	resp, err := hgapi.Exchange(ctx, s.client, &hgapi.Request{Method: "GET", URL: u.String(), Headers: headers, CredentialsManaged: true})
	// Never surface URLs: they contain the short-lived QR authorization token.
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrTemporary
	}
	return resp, nil
}

func (s *Session) call(ctx context.Context, path string, params url.Values) (map[string]any, error) {
	var data map[string]any
	var err error
	for i := 0; i < 3; i++ {
		if i > 0 {
			timer := time.NewTimer(time.Duration(i) * 300 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		data, err = s.callOnce(ctx, path, params)
		if !errors.Is(err, ErrTemporary) {
			return data, err
		}
	}
	return nil, err
}

func (s *Session) callOnce(ctx context.Context, path string, params url.Values) (map[string]any, error) {
	resp, err := s.request(ctx, path, params)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w（HTTP %d）", ErrTemporary, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("红果登录服务暂不可用（HTTP %d）", resp.StatusCode)
	}
	raw := resp.Body
	if len(raw) > 2<<20 {
		return nil, ErrTemporary
	}
	var env struct {
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&env) != nil || env.Message != "success" || env.Data == nil {
		return nil, errors.New("红果登录校验未通过，请重新扫码")
	}
	if code, ok := env.Data["error_code"]; ok && stringValue(code) != "0" {
		return nil, errors.New("红果登录校验未通过，请重新扫码")
	}
	return env.Data, nil
}

func (s *Session) finishRedirect(ctx context.Context, value string) error {
	base, _ := url.Parse(Origin)
	u, err := base.Parse(value)
	for i := 0; i < 5; i++ {
		if err != nil || u == nil || u.Scheme != "https" || u.Host != "hongguoduanju.com" || u.User != nil {
			return errors.New("登录确认地址无效，请重新扫码")
		}
		resp, e := hgapi.Exchange(ctx, s.client, &hgapi.Request{Method: "GET", URL: u.String(), Headers: map[string]string{"User-Agent": userAgent, "Referer": Origin + "/"}, CredentialsManaged: true})
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrTemporary
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return ErrTemporary
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			u, err = u.Parse(resp.Headers["Location"])
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		return fmt.Errorf("登录确认失败（HTTP %d）", resp.StatusCode)
	}
	return errors.New("登录确认跳转次数异常，请重新扫码")
}
func stringValue(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	}
	return ""
}
