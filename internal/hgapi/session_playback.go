package hgapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"myguo/internal/hgapi/model"
	"myguo/internal/sign"
	"myguo/internal/upstream"
)

const sessionTokenPath = "/sdi/get_token"

type signingSession struct {
	token   sign.SDIToken
	loading chan struct{}
	err     error
	retryAt time.Time
}

type playbackSessions struct {
	mu      sync.Mutex
	entries map[string]*signingSession
	counter atomic.Uint64
	enabled bool
	origin  string
}

type boundPlaybackIdentity struct {
	device                                                   map[string]string
	userAgent, cookies, handshakeCookies, token, region, key string
}

func (c *Client) bindPlaybackSession(p *DeviceProfile) boundPlaybackIdentity {
	d := c.deviceOf(p)
	values := map[string]string{
		"device_platform": "android", "os": "android", "ssmix": "a", "channel": "update_64",
		"aid": "8662", "app_name": "novelread", "version_code": "74332", "version_name": "7.4.3.32",
		"manifest_version_code": "74332", "update_version_code": "74332", "language": "zh", "ac": "wifi",
		"host_abi": "arm64-v8a", "dragon_device_type": "phone", "pv_player": "74332", "compliance_status": "0",
		"need_personal_recommend": "1", "player_so_load": "1", "is_android_pad_screen": "0", "klink_egdi": "",
		"device_id": p.DeviceID, "iid": p.InstallID, "cdid": p.CDID,
		"device_brand": d.Brand, "device_type": d.Model, "os_version": d.OSVersion, "os_api": strconv.Itoa(d.OSAPI),
		"resolution": d.Resolution, "dpi": strconv.Itoa(d.DPI), "rom_version": d.Build,
	}
	s := boundPlaybackIdentity{device: values, region: p.StoreRegion}
	s.userAgent = fmt.Sprintf("com.phoenix.read/74332 (Linux; U; Android %s; zh_CN; %s; Build/%s; Cronet/TTNetVersion:8d40f833 2026-03-03 QuicVersion:21ac1950 2025-11-18)", d.OSVersion, d.Model, d.Build)
	if source, ok := c.transport.(CredentialSource); ok && !p.Isolated {
		s.cookies, s.token, s.region = source.Credentials()
	}
	if s.cookies == "" {
		s.cookies = guestCookie(p)
	}
	s.cookies = replaceCookie(s.cookies, "install_id", p.InstallID)
	s.handshakeCookies = "passport_csrf_token=; passport_csrf_token_default=; store-region=cn-gd; store-region-src=did; install_id=" + p.InstallID
	if p.TTReq != "" {
		s.handshakeCookies += "; ttreq=" + p.TTReq
	}
	profileBytes, _ := json.Marshal(values)
	digest := sha256.Sum256(append(profileBytes, []byte("\x00"+s.cookies+"\x00"+s.token+"\x00"+s.region)...))
	s.key = hex.EncodeToString(digest[:])
	return s
}

func sessionQuery(s boundPlaybackIdentity, handshake bool) [][2]string {
	keys := []string{"device_platform", "os", "ssmix", "_rticket", "cdid", "channel", "aid", "app_name", "version_code", "version_name", "manifest_version_code", "update_version_code", "resolution", "dpi", "device_type", "device_brand", "language", "os_api", "os_version", "ac", "host_abi", "dragon_device_type", "pv_player", "compliance_status", "need_personal_recommend", "player_so_load", "is_android_pad_screen", "rom_version", "iid", "device_id", "klink_egdi"}
	extra := map[string]string{}
	if handshake {
		keys = append([]string{"lc_id", "platform", "device_platform", "sdk_ver", "sdk_ver_code", "app_ver", "version_code", "aid", "sdkid", "subaid", "iid", "did", "bd_did", "client_type", "region_type", "mode", "full_update_version_code"}, keys[1:]...)
		extra = map[string]string{"lc_id": "646637794", "platform": "android", "sdk_ver": "v04.10.03-ml-android", "sdk_ver_code": "67764992", "app_ver": "7.4.3.32", "did": s.device["device_id"], "client_type": "inhouse", "region_type": "ml", "mode": "2", "full_update_version_code": "74332"}
	}
	pairs := make([][2]string, 0, len(keys))
	for i, key := range keys {
		if handshake && i >= 17 && (key == "aid" || key == "iid" || key == "version_code") {
			continue
		}
		v := s.device[key]
		if value, ok := extra[key]; ok {
			v = value
		}
		pairs = append(pairs, [2]string{key, v})
	}
	return pairs
}

func (c *Client) sessionRequest(s boundPlaybackIdentity, token string, body []byte, origin, path string, handshake bool) (*Request, error) {
	query, headers, err := sign.SignSessionV5(sessionQuery(s, handshake), c.clock().UnixMilli(), body, sign.SessionParams{Device: s.device, Token: token, Counter: c.sessions.counter.Add(1), Handshake: handshake})
	if err != nil {
		return nil, err
	}
	headers["User-Agent"] = s.userAgent
	headers["Content-Type"] = "application/json; charset=utf-8"
	headers["Accept"] = "application/json; charset=utf-8,application/x-protobuf"
	headers["x-xs-from-web"] = "0"
	headers["sdk-version"] = "2"
	headers["passport-sdk-version"] = "5051452"
	headers["x-vc-bdturing-sdk-version"] = "4.0.3.cn"
	headers["x-ss-dp"] = "8662"
	headers["lc"] = "101"
	headers["Cookie"] = s.cookies
	if !handshake {
		headers["Content-Encoding"] = "gzip"
		if s.token != "" {
			headers["x-tt-token"] = s.token
		}
		if s.region != "" {
			headers["x-tt-store-region"] = s.region
		}
	} else {
		headers["Cookie"] = s.handshakeCookies
		headers["Content-Type"] = "application/octet-stream"
		headers["Accept"] = "*/*"
		headers["x-tt-request-tag"] = "t=0;n=0"
	}
	return &Request{Method: "POST", URL: strings.TrimRight(origin, "/") + path + "?" + query, Headers: headers, Body: body, CredentialsManaged: true}, nil
}

func (c *Client) acquirePlaybackSession(ctx context.Context, s boundPlaybackIdentity) (sign.SDIToken, error) {
	for {
		if err := ctx.Err(); err != nil {
			return sign.SDIToken{}, err
		}
		c.sessions.mu.Lock()
		entry := c.sessions.entries[s.key]
		if entry == nil {
			entry = &signingSession{}
			c.sessions.entries[s.key] = entry
		}
		if entry.loading != nil {
			done := entry.loading
			c.sessions.mu.Unlock()
			select {
			case <-ctx.Done():
				return sign.SDIToken{}, ctx.Err()
			case <-done:
				continue
			}
		}
		if entry.token.Value != "" && entry.token.Status == 200 {
			token := entry.token
			c.sessions.mu.Unlock()
			return token, nil
		}
		if entry.err != nil && c.clock().Before(entry.retryAt) {
			err := entry.err
			c.sessions.mu.Unlock()
			return sign.SDIToken{}, err
		}
		done := make(chan struct{})
		entry.loading = done
		previous := entry.token.Value
		c.sessions.mu.Unlock()
		token, err := c.exchangePlaybackSession(ctx, s, previous)
		if err == nil && token.Status == 202 {
			token, err = c.exchangePlaybackSession(ctx, s, token.Value)
		}
		if err == nil && token.Status != 200 {
			err = sign.ErrSDI
		}
		c.sessions.mu.Lock()
		// A manual retry can detach this entry while its old request finishes.
		if c.sessions.entries[s.key] == entry {
			entry.err = err
			entry.retryAt = time.Time{}
			if err == nil {
				entry.token = token
			} else if ctx.Err() == nil {
				entry.retryAt = c.clock().Add(15 * time.Second)
			}
		}
		entry.loading = nil
		close(done)
		c.sessions.mu.Unlock()
		return token, err
	}
}

func (c *Client) exchangePlaybackSession(ctx context.Context, s boundPlaybackIdentity, previous string) (sign.SDIToken, error) {
	body, err := sign.EncodeSDI(sign.BuildSDIPayload(s.device, previous), c.clock().UnixMilli())
	if err != nil {
		return sign.SDIToken{}, err
	}
	req, err := c.sessionRequest(s, previous, body, c.sessions.origin, sessionTokenPath, true)
	if err != nil {
		return sign.SDIToken{}, err
	}
	response, err := c.transport.Do(upstream.WithFixedHost(ctx), req)
	if ctx.Err() != nil {
		return sign.SDIToken{}, ctx.Err()
	}
	if err != nil {
		if ctx.Err() != nil {
			return sign.SDIToken{}, ctx.Err()
		}
		return sign.SDIToken{}, &transportError{cause: err}
	}
	if response == nil {
		return sign.SDIToken{}, ErrTransport
	}
	if response.StatusCode != 200 {
		_, err = readResponse(response)
		if err == nil {
			err = &APIError{HTTPStatus: response.StatusCode}
		}
		return sign.SDIToken{}, err
	}
	raw, err := responseBytes(response)
	if err != nil {
		return sign.SDIToken{}, err
	}
	return sign.DecodeSDI(raw)
}

func (c *Client) callSessionPlayback(ctx context.Context, p *DeviceProfile, ids []string, body []byte, opts PlaybackOptions) (map[string]*model.Playback, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	s := c.bindPlaybackSession(p)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	biz, _ := payload["biz_param"].(map[string]any)
	if biz == nil {
		return nil, ErrParamInvalid
	}
	biz["disable_video_relate_book"] = false
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	gz, err := GzipBody(raw)
	if err != nil {
		return nil, err
	}
	origin := accountOrigin
	if opts.Origin != "" {
		origin = opts.Origin
	} else if u, _ := url.Parse(c.apiOrigin); u != nil && !strings.HasSuffix(u.Hostname(), ".fqnovel.com") {
		origin = c.apiOrigin
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.acquirePlaybackSession(ctx, s)
		if err != nil {
			return nil, err
		}
		req, err := c.sessionRequest(s, token.Value, gz, origin, PlaybackPath, false)
		if err != nil {
			return nil, err
		}
		response, err := c.transport.Do(upstream.WithFixedHost(ctx), req)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &transportError{cause: err}
		}
		// Only an actual empty HTTP-200 response spends the reacquisition budget.
		if response != nil && response.StatusCode == 200 {
			decoded, decodeErr := responseBytes(response)
			if decodeErr == nil && len(decoded) == 0 {
				c.sessions.mu.Lock()
				if entry := c.sessions.entries[s.key]; entry != nil && entry.token == token {
					delete(c.sessions.entries, s.key)
				}
				c.sessions.mu.Unlock()
				if attempt == 0 {
					continue
				}
				return nil, ErrAnonymousLimited
			}
		}
		raw, err := readResponse(response)
		if err != nil {
			return nil, err
		}
		models, err := ParseVideoModels(raw, ids)
		if err != nil {
			return nil, err
		}
		for id, pb := range models {
			models[id], err = c.compatiblePlayback(ctx, pb)
			if err != nil {
				return nil, err
			}
		}
		return models, nil
	}
	return nil, ErrAnonymousLimited
}

// RetryPlaybackNow drops signing failures and tokens, including an in-flight
// entry's right to repopulate the cache. Catalogue/account identities stay bound.
func (c *Client) RetryPlaybackNow(_ string) {
	c.sessions.mu.Lock()
	c.sessions.entries = make(map[string]*signingSession)
	c.sessions.mu.Unlock()
	c.ClearPlaybackCache()
}

// ResolveSessionPlayback runs only the SDI channel for protocol diagnostics.
func (c *Client) ResolveSessionPlayback(ctx context.Context, id string, opts PlaybackOptions) (*model.Playback, error) {
	body, err := PlaybackBody([]string{id}, 1004)
	if err != nil {
		return nil, err
	}
	p := opts.Profile
	if p == nil {
		p, err = c.playbackIdentity(ctx)
		if err != nil {
			return nil, err
		}
	}
	models, err := c.callSessionPlayback(ctx, p, []string{id}, body, opts)
	if err != nil {
		return nil, err
	}
	pb := models[id]
	if pb == nil {
		return nil, ErrAnonymousLimited
	}
	if pb.Protected {
		return pb, ErrDRMProtected
	}
	return pb, nil
}
