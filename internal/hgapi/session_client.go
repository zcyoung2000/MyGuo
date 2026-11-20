package hgapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"myguo/internal/hgapi/protocol"
	"myguo/internal/sign"
)

// Session is an immutable request identity. The business owner checks its
// account scope before supplying it; API calls never read a newer global login
// half way through signing or send another account's credentials on retry.
type Session struct {
	DeviceID, InstallID, CDID, OpenUDID    string
	Cookies, Token, UserAgent, StoreRegion string
	Guest                                  bool
}

// SessionClient exposes typed endpoints for a bound app session. Client owns
// guest registration/catalog and the dedicated playback profile; this handle
// owns account and interaction requests using the same HTTP boundary.
type SessionClient struct {
	http    *http.Client
	session Session
}

func NewSessionClient(client *http.Client, session Session) *SessionClient {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &SessionClient{http: client, session: session}
}

func GuestCookies(p *DeviceProfile) string {
	return "passport_csrf_token=; passport_csrf_token_default=; store-region=" + p.StoreRegion + "; store-region-src=" + p.StoreRegionSrc + "; install_id=" + p.InstallID + "; ttreq=" + p.TTReq
}

func (c *SessionClient) requireAccount() error {
	if c.session.Guest || c.session.Cookies == "" {
		return ErrAuthenticationRequired
	}
	return nil
}

func validContentID(id string) bool {
	if len(id) < 6 || len(id) > 30 {
		return false
	}
	for _, char := range id {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

const (
	accountOrigin  = "https://api5-normal-sinfonlinea.fqnovel.com"
	commentOrigin  = "https://api5-normal-sinfonlineb.fqnovel.com"
	mutationOrigin = "https://api5-normal-lq.fqnovel.com"
	relatedOrigin  = "https://api3-normal-lf.fqnovel.com"
	appVersion     = "7.3.9.32"
)

type signing uint8

const (
	unsigned signing = iota
	getSigned
	postSigned
)

type endpoint struct {
	operation, origin, path, method string
	sign                            signing
	limit                           int
}

func (c *SessionClient) call(ctx context.Context, ep endpoint, pairs [][2]string, body []byte, headers map[string]string) ([]byte, error) {
	ticket := time.Now().UnixMilli()
	var query string
	var signed map[string]string
	switch ep.sign {
	case getSigned:
		query, signed = sign.SignGET(pairs, ticket, c.session.DeviceID, appVersion, nil)
	case postSigned:
		query, signed = sign.SignPOST(ep.path, pairs, ticket, c.session.DeviceID, appVersion, body, nil)
	default:
		values := url.Values{}
		for _, pair := range pairs {
			values.Add(pair[0], pair[1])
		}
		query = values.Encode()
	}
	if signed == nil {
		signed = make(map[string]string)
	}
	signed["User-Agent"] = c.session.UserAgent
	signed["Accept"] = "application/json"
	signed["x-ss-req-ticket"] = strconv.FormatInt(ticket, 10)
	if c.session.Cookies != "" {
		signed["Cookie"] = c.session.Cookies
	}
	if c.session.Token != "" {
		signed["x-tt-token"] = c.session.Token
	}
	if c.session.StoreRegion != "" {
		signed["x-tt-store-region"] = c.session.StoreRegion
	}
	for key, value := range headers {
		signed[key] = value
	}
	rawURL := ep.origin + ep.path
	if query != "" {
		rawURL += "?" + query
	}
	// Exactly one send: mutations have durable business queues and must not be
	// replayed by a generic API retry loop after an ambiguous network failure.
	response, err := Exchange(ctx, c.http, &Request{Method: ep.method, URL: rawURL, Headers: signed, Body: body, CredentialsManaged: true})
	if err != nil {
		return nil, fmt.Errorf("%s请求失败: %w", ep.operation, err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, &APIError{HTTPStatus: response.StatusCode, Message: ep.operation}
	}
	raw, err := responseBytes(response)
	if err != nil {
		return nil, fmt.Errorf("%s响应读取失败: %w", ep.operation, err)
	}
	if ep.limit > 0 && len(raw) > ep.limit {
		return nil, fmt.Errorf("%s响应过大", ep.operation)
	}
	return raw, nil
}

func (c *SessionClient) devicePairs(extra ...[2]string) [][2]string {
	s := c.session
	return append(protocol.AppDeviceQueryPairs(s.DeviceID, s.InstallID, s.CDID, s.OpenUDID), extra...)
}

func (c *SessionClient) comment(ctx context.Context, path, source, channel string, payload any, limit int) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return c.call(ctx, endpoint{"评论", commentOrigin, path, http.MethodPost, postSigned, limit}, c.devicePairs(), body, map[string]string{"Comment-Source": source, "Server-Channel": channel, "Content-Type": "application/json; charset=utf-8"})
}

var ErrParameterInvalid = errors.New("parameter invalid")

func (c *SessionClient) mutation(ctx context.Context, operation, path string, payload any) error {
	if err := c.requireAccount(); err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err = writer.Write(raw); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	var nonce [4]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	body, err := c.call(ctx, endpoint{operation, mutationOrigin, path, http.MethodPost, postSigned, 1 << 20}, c.devicePairs(), compressed.Bytes(), map[string]string{"Content-Encoding": "gzip", "X-SS-DP": "8662", "Lc": "101", "X-Reading-Request": fmt.Sprintf("%d-%d", time.Now().UnixMilli(), binary.LittleEndian.Uint32(nonce[:]))})
	if err != nil {
		return err
	}
	var result struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("%s接口返回无效 JSON: %w", operation, err)
	}
	if result.Code == nil {
		return fmt.Errorf("%s接口缺少业务状态", operation)
	}
	if *result.Code == 100103 {
		return fmt.Errorf("%s接口返回 code %d: %w", operation, *result.Code, ErrParameterInvalid)
	}
	if *result.Code != 0 {
		return &APIError{Code: strconv.Itoa(*result.Code), Message: result.Message}
	}
	return nil
}
