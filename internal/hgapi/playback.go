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
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	drm "myguo/internal/hgapi/keymaterial"
	"myguo/internal/hgapi/model"
	"myguo/internal/hgapi/protocol"
	"myguo/internal/sign"
	"myguo/internal/upstream"
)

const PlaybackPath = "/novel/player/multi_video_model/player/v1"
const PlaybackBatchSize = 15

type PlaybackOptions struct {
	Mode    *sign.Mode
	Origin  string
	Profile *DeviceProfile
}
type playbackLoad struct{ done chan struct{} }

type CredentialSource interface {
	Credentials() (cookies, token, region string)
}

func PlaybackBody(ids []string, contentType int) ([]byte, error) {
	if len(ids) == 0 || len(ids) > PlaybackBatchSize {
		return nil, fmt.Errorf("playback batch requires 1..15 IDs")
	}
	for _, id := range ids {
		if !validID(id) {
			return nil, fmt.Errorf("invalid episode ID")
		}
	}
	return json.Marshal(map[string]any{"dr_scene": "player", "biz_param": map[string]any{"detail_page_version": 0, "device_level": 4, "disable_digg_stat": false, "need_all_video_definition": true, "need_mp4_align": false, "use_os_player": false, "use_server_dns": false, "video_platform": 1024}, "mixed_video_id_map": map[string][]string{strconv.Itoa(contentType): ids}})
}

func PlaybackQuery(p *DeviceProfile) [][2]string {
	profile := sign.PlaybackProfile
	return [][2]string{{"aid", "8662"}, {"app_name", profile.AppName}, {"version_code", "71332"}, {"version_name", profile.VersionName}, {"manifest_version_code", "71332"}, {"update_version_code", "71332"}, {"channel", profile.Channel}, {"device_platform", "android"}, {"os", "android"}, {"ac", "wifi"}, {"ssmix", "a"}, {"language", "zh"}, {"device_brand", "Redmi"}, {"device_type", "25053RT47C"}, {"os_version", "16"}, {"os_api", "36"}, {"resolution", "1280*2772"}, {"dpi", "520"}, {"host_abi", "arm64-v8a"}, {"dragon_device_type", "phone"}, {"pv_player", "71332"}, {"compliance_status", "0"}, {"need_personal_recommend", "1"}, {"player_so_load", "1"}, {"is_android_pad_screen", "0"}, {"device_id", p.DeviceID}, {"iid", p.InstallID}}
}

func (c *Client) ClearPlaybackCache() {
	c.playMu.Lock()
	c.playCache = make(map[string]*model.Playback)
	c.playGoodAt = time.Time{}
	c.preferTabUntil = time.Time{}
	c.playMu.Unlock()
}

func (c *Client) ResolvePlayback(ctx context.Context, id string, opts PlaybackOptions) (*model.Playback, error) {
	models, err := c.MultiVideoModels(ctx, []string{id}, opts)
	if err != nil {
		return nil, err
	}
	pb := models[id]
	if pb == nil {
		return nil, fmt.Errorf("%w: requested episode missing", ErrAnonymousLimited)
	}
	if pb.Protected {
		return pb, ErrDRMProtected
	}
	return pb, nil
}

func (c *Client) MultiVideoModels(ctx context.Context, ids []string, opts PlaybackOptions) (map[string]*model.Playback, error) {
	out := make(map[string]*model.Playback, len(ids))
	for start := 0; start < len(ids); start += PlaybackBatchSize {
		batch := ids[start:min(start+PlaybackBatchSize, len(ids))]
		models, err := c.shortPlaybackModels(ctx, batch, opts)
		if err != nil {
			return nil, err
		}
		for id, pb := range models {
			out[id] = pb
		}
	}
	return out, nil
}

func (c *Client) shortPlaybackModels(ctx context.Context, ids []string, opts PlaybackOptions) (map[string]*model.Playback, error) {
	// Validate before either route; a malformed request never starts recovery.
	body, err := PlaybackBody(ids, 1004)
	if err != nil {
		return nil, err
	}
	c.playMu.Lock()
	preferTab := opts.Mode == nil && c.clock().Before(c.preferTabUntil)
	c.playMu.Unlock()
	if preferTab {
		models, tabErr := c.videoTabModels(ctx, ids, opts)
		if tabErr == nil {
			return models, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c.setVideoTabPreference(false)
	}
	p := opts.Profile
	if p == nil {
		p, err = c.playbackIdentity(ctx)
		if err != nil {
			return nil, err
		}
	}
	models, postErr := c.callPlayback(ctx, p, ids, body, opts)
	var code *CodeError
	if errors.As(postErr, &code) && code.Code == 101000 && ctx.Err() == nil {
		body, _ = PlaybackBody(ids, 1001)
		models, postErr = c.callPlayback(ctx, p, ids, body, opts)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if postErr == nil || playbackRequestTerminal(postErr) {
		return models, postErr
	}
	if opts.Mode != nil {
		if playbackRecoverable(postErr) && c.deviceResets > 0 {
			return c.recoverPlaybackModels(ctx, p, ids, body, opts)
		}
		return nil, postErr
	}
	models, tabErr := c.videoTabModels(ctx, ids, opts)
	if tabErr == nil {
		c.setVideoTabPreference(true)
		return models, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// Try the distinct GET protocol before replacing a registered identity.
	// This keeps a working GET route out of device-reset loops and preserves
	// request time for it when the POST path is unhealthy.
	if playbackRecoverable(postErr) && c.deviceResets > 0 && !playbackRequestTerminal(tabErr) && !errors.Is(tabErr, ErrVideoTabEpisodeMissing) {
		models, recoveryErr := c.recoverPlaybackModels(ctx, p, ids, body, opts)
		if recoveryErr == nil {
			return models, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if playbackRequestTerminal(recoveryErr) {
			return nil, recoveryErr
		}
		// A freshly registered playback identity is also a GET candidate.
		models, freshTabErr := c.videoTabModels(ctx, ids, opts)
		if freshTabErr == nil {
			c.setVideoTabPreference(true)
			return models, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.Join(postErr, fmt.Errorf("video_tab: %w", tabErr), recoveryErr, fmt.Errorf("video_tab recovery: %w", freshTabErr))
	}
	return nil, errors.Join(postErr, fmt.Errorf("video_tab: %w", tabErr))
}

func (c *Client) setVideoTabPreference(prefer bool) {
	c.playMu.Lock()
	defer c.playMu.Unlock()
	c.preferTabUntil = time.Time{}
	if prefer {
		c.preferTabUntil = c.clock().Add(videoTabPreferTTL)
	}
}

func playbackRequestTerminal(err error) bool {
	var code *CodeError
	if errors.As(err, &code) && code.Code == 101000 {
		// This player code requests the 1001 content-type compatibility path.
		// The generic catalogue classifier treats the whole 1010xx range as
		// unavailable content, which must not cancel another playback host.
		return false
	}
	return errors.Is(err, ErrAuthenticationRequired) || errors.Is(err, ErrRateLimited) ||
		errors.Is(err, ErrChallengeRequired) || errors.Is(err, ErrParamInvalid) ||
		errors.Is(err, ErrContentUnavailable)
}

func (c *Client) recoverPlaybackModels(ctx context.Context, p *DeviceProfile, ids []string, body []byte, opts PlaybackOptions) (map[string]*model.Playback, error) {
	// A recovery identity belongs to playback. It never resets the catalogue
	// profile, saved feed cursor or the user's account as a side effect.
	c.playFreshMu.Lock()
	defer c.playFreshMu.Unlock()
	c.playMu.Lock()
	fresh := c.playDevice
	c.playMu.Unlock()
	if fresh == nil || fresh.DeviceID == p.DeviceID {
		var err error
		fresh, err = NewAnonymousDeviceProfile(nil, c.clock)
		if err != nil {
			return nil, err
		}
		d := model.Device{Brand: "Redmi", Model: "25053RT47C", OSVersion: "16", OSAPI: 36, Build: "BP2A.250605.031.A3", Resolution: "1280*2772", DPI: 520, Channel: "update_64"}
		fresh.Device = &d
		fresh.Isolated = true
		if err = c.register(ctx, fresh); err != nil {
			return nil, err
		}
		c.playMu.Lock()
		c.playDevice = fresh
		c.playMu.Unlock()
	}
	return c.callPlayback(ctx, fresh, ids, body, opts)
}

func (c *Client) playbackIdentity(ctx context.Context) (*DeviceProfile, error) {
	path := c.playbackDeviceFile
	if env := os.Getenv("HG_PLAYBACK_DEVICE_FILE"); env != "" {
		path = env
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			var p DeviceProfile
			if err = json.Unmarshal(b, &p); err != nil {
				return nil, fmt.Errorf("invalid playback device file")
			}
			var fields struct {
				DeviceID string `json:"device_id"`
				IID      string `json:"install_id"`
			}
			_ = json.Unmarshal(b, &fields)
			if p.DeviceID == "" {
				p.DeviceID = fields.DeviceID
			}
			if p.InstallID == "" {
				p.InstallID = fields.IID
			}
			if !validID(p.DeviceID) || !validID(p.InstallID) {
				return nil, fmt.Errorf("playback device file lacks identity")
			}
			p.Isolated = true
			return &p, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("cannot read playback device file")
		}
	}
	return c.ensureGuest(ctx)
}

func playbackRecoverable(err error) bool {
	if errors.Is(err, ErrAnonymousLimited) || errors.Is(err, ErrDeviceRejected) || errors.Is(err, ErrTransport) {
		return true
	}
	var e *CodeError
	return errors.As(err, &e) && e.Code == 110001
}

func (c *Client) callPlayback(ctx context.Context, p *DeviceProfile, ids []string, body []byte, opts PlaybackOptions) (map[string]*model.Playback, error) {
	mode := sign.Full
	if opts.Mode != nil {
		mode = *opts.Mode
	}
	modes := []bool{false}
	if mode == sign.None {
		modes = []bool{false, true}
		c.playMu.Lock()
		if c.playGoodMode && c.clock().Sub(c.playGoodAt) < 10*time.Minute {
			modes = []bool{true, false}
		}
		c.playMu.Unlock()
	}
	var last error
	for _, unsigned := range modes {
		models, err := c.racePlaybackHosts(ctx, p, ids, body, mode, unsigned, opts.Origin)
		if err == nil {
			c.playMu.Lock()
			c.playGoodMode = unsigned
			c.playGoodAt = c.clock()
			c.playMu.Unlock()
			return models, nil
		}
		last = err
		if !playbackRecoverable(err) {
			return nil, err
		}
	}
	return nil, last
}

func (c *Client) playbackHosts() []string {
	u, _ := url.Parse(c.apiOrigin)
	if u == nil || !strings.HasSuffix(u.Hostname(), ".fqnovel.com") {
		return []string{c.apiOrigin}
	}
	result := []string{c.apiOrigin}
	for _, host := range upstream.DefaultPlaybackHosts {
		origin := "https://" + host
		if origin != c.apiOrigin {
			result = append(result, origin)
		}
	}
	return result[:min(3, len(result))]
}

func (c *Client) racePlaybackHosts(ctx context.Context, p *DeviceProfile, ids []string, body []byte, mode sign.Mode, unsigned bool, originOverride string) (map[string]*model.Playback, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	type result struct {
		models map[string]*model.Playback
		err    error
	}
	hosts := c.playbackHosts()
	if originOverride != "" {
		hosts = []string{strings.TrimRight(originOverride, "/")}
	}
	results := make(chan result, len(hosts))
	for _, host := range hosts {
		go func(origin string) {
			request, err := c.playbackRequest(p, body, mode, unsigned, origin)
			if err != nil {
				results <- result{err: err}
				return
			}
			raw, _, err := c.send(upstream.WithFixedHost(ctx), request, 1)
			if err != nil {
				results <- result{err: err}
				return
			}
			models, err := ParseVideoModels(raw, ids)
			if err == nil {
				for id, pb := range models {
					models[id], err = c.compatiblePlayback(ctx, pb)
					if err != nil {
						break
					}
				}
			}
			results <- result{models: models, err: err}
		}(host)
	}
	var failures []error
	for range hosts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r := <-results:
			if r.err == nil && len(r.models) > 0 {
				return r.models, nil
			}
			if r.err != nil && playbackRequestTerminal(r.err) {
				return nil, r.err
			}
			if r.err == nil {
				r.err = ErrAnonymousLimited
			}
			failures = append(failures, r.err)
		}
	}
	return nil, errors.Join(failures...)
}

func replaceCookie(raw, name, value string) string {
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts)+1)
	for _, p := range parts {
		key, _, _ := strings.Cut(strings.TrimSpace(p), "=")
		if key != "" && key != name {
			out = append(out, strings.TrimSpace(p))
		}
	}
	if value != "" {
		out = append(out, name+"="+value)
	}
	return strings.Join(out, "; ")
}

func (c *Client) playbackRequest(p *DeviceProfile, body []byte, mode sign.Mode, unsigned bool, origin string) (*Request, error) {
	copy := *p
	if unsigned {
		c.playMu.Lock()
		if c.unsignedDevice == nil || c.clock().Sub(c.unsignedAt) >= 10*time.Minute {
			c.unsignedDevice = &DeviceProfile{DeviceID: randomPlayerID(), InstallID: randomPlayerID()}
			c.unsignedAt = c.clock()
		}
		copy.DeviceID, copy.InstallID = c.unsignedDevice.DeviceID, c.unsignedDevice.InstallID
		c.playMu.Unlock()
	}
	query, headers, err := sign.SignProfile(sign.PlaybackProfile, mode, origin, PlaybackPath, PlaybackQuery(&copy), c.clock().UnixMilli(), copy.DeviceID, body, nil)
	if err != nil {
		return nil, err
	}
	headers["Content-Type"] = "application/json; charset=utf-8"
	headers["Accept"] = "application/json; charset=utf-8,application/x-protobuf"
	headers["User-Agent"] = sign.PlaybackProfile.UserAgent
	headers["x-ss-dp"] = "8662"
	headers["lc"] = "101"
	if !unsigned {
		cookies, token, region := "", "", ""
		if source, ok := c.transport.(CredentialSource); ok {
			cookies, token, region = source.Credentials()
		}
		if p.Isolated {
			cookies, token = "", ""
		}
		if cookies == "" {
			cookies = guestCookie(p)
		}
		headers["Cookie"] = replaceCookie(cookies, "install_id", p.InstallID)
		if token != "" {
			headers["x-tt-token"] = token
		}
		if region != "" {
			headers["x-tt-store-region"] = region
		}
	}
	return &Request{Method: "POST", URL: origin + PlaybackPath + "?" + query, Headers: headers, Body: append([]byte{}, body...), SkipAuth: unsigned, CredentialsManaged: true}, nil
}

func randomPlayerID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return strconv.FormatUint(1_000_000_000_000_000+binary.LittleEndian.Uint64(b[:])%8_000_000_000_000_000, 10)
}

func unwrapModel(raw json.RawMessage) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, ErrAnonymousLimited
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return []byte(s), nil
	}
	return raw, nil
}

func ParseVideoModels(raw []byte, ids []string) (map[string]*model.Playback, error) {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("invalid playback response")
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &entries); err != nil {
		return nil, fmt.Errorf("invalid playback data")
	}
	for _, name := range []string{"video_model_data", "1004", "1001"} {
		if b := entries[name]; len(b) > 0 {
			var nested map[string]json.RawMessage
			if json.Unmarshal(b, &nested) == nil {
				entries = nested
				break
			}
		}
	}
	out := make(map[string]*model.Playback, len(ids))
	for _, id := range ids {
		item := entries[id]
		if len(item) == 0 {
			continue
		}
		var entry struct {
			VideoModel json.RawMessage `json:"video_model"`
		}
		if json.Unmarshal(item, &entry) != nil {
			continue
		}
		body, err := unwrapModel(entry.VideoModel)
		if err != nil {
			continue
		}
		wrapped, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"video_model": json.RawMessage(body)}})
		pb, err := protocol.ParsePlayback(wrapped, id)
		var meta struct {
			Fallback json.RawMessage `json:"fallback_api"`
			KeySeed  string          `json:"key_seed"`
		}
		_ = json.Unmarshal(body, &meta)
		fallback := ""
		_ = json.Unmarshal(meta.Fallback, &fallback)
		if fallback == "" {
			var f struct {
				URL string `json:"fallback_api"`
			}
			_ = json.Unmarshal(meta.Fallback, &f)
			fallback = f.URL
		}
		if err != nil && fallback == "" {
			continue
		}
		if pb == nil {
			pb = &model.Playback{EpisodeID: id}
		}
		pb.FallbackAPI = fallback
		pb.KeySeed = meta.KeySeed
		if pb.ExpiresAt == 0 {
			pb.ExpiresAt = time.Now().Add(10 * time.Minute).Unix()
		}
		out[id] = pb
	}
	if len(out) == 0 {
		return nil, ErrAnonymousLimited
	}
	return out, nil
}

func (c *Client) compatiblePlayback(ctx context.Context, pb *model.Playback) (*model.Playback, error) {
	compatible := false
	for i := range pb.Streams {
		stream := &pb.Streams[i]
		if pb.KeySeed != "" {
			u, err := DecryptSpadeURL(stream.URL, pb.KeySeed)
			if err != nil {
				return nil, err
			}
			stream.URL = u
		}
		codec := strings.ToLower(stream.Codec)
		if codec != "bytevc2" {
			compatible = true
		}
	}
	if compatible {
		return pb, nil
	}
	if pb.FallbackAPI == "" {
		return nil, fmt.Errorf("playback has no compatible stream")
	}
	u, err := url.Parse(pb.FallbackAPI)
	if err != nil || u.Host == "" || u.Scheme != "https" || u.User != nil {
		return nil, fmt.Errorf("invalid fallback API")
	}
	raw, _, err := c.send(ctx, &Request{Method: "GET", URL: u.String(), Headers: map[string]string{"User-Agent": sign.PlaybackProfile.UserAgent}, SkipAuth: true}, 1)
	if err != nil {
		return nil, err
	}
	var nested struct {
		VideoInfo json.RawMessage `json:"video_info"`
	}
	_ = json.Unmarshal(raw, &nested)
	if len(nested.VideoInfo) > 0 {
		raw = nested.VideoInfo
	}
	parsed, err := protocol.ParsePlayback(raw, pb.EpisodeID)
	if err != nil {
		return nil, fmt.Errorf("fallback has no playable model")
	}
	parsed.FallbackAPI = ""
	return parsed, nil
}

func ContentKey(pb *model.Playback) (model.ContentKey, error) {
	if pb == nil {
		return model.ContentKey{}, fmt.Errorf("missing playback model")
	}
	if !pb.Protected {
		return model.ContentKey{}, ErrNotProtected
	}
	k, err := drm.Derive(pb.KeyMaterial)
	if err != nil {
		return model.ContentKey{}, fmt.Errorf("%w: unrecognized key material", ErrDRMProtected)
	}
	return model.ContentKey{Hex: k.Hex, Version: k.Version}, nil
}

// GzipBody is used only by APIs whose profile permits compressed request bodies.
// The reference batch player defaults to PlainBody=true.
func GzipBody(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(body); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sortedStreams(streams []model.Stream) {
	sort.SliceStable(streams, func(i, j int) bool { return streams[i].Bitrate > streams[j].Bitrate })
}
