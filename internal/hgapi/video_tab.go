package hgapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"myguo/internal/hgapi/model"
	"myguo/internal/hgapi/protocol"
	"myguo/internal/sign"
	"myguo/internal/upstream"
)

const (
	VideoTabPath      = "/reading/bookapi/video_tab/multi_video_model/v1/"
	VideoTabOrigin    = "https://api5-normal-sinfonlineb.fqnovel.com"
	VideoTabUserAgent = "com.phoenix.read/73932 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)"
	videoTabTimeout   = 30 * time.Second
	videoTabPreferTTL = 30 * time.Minute
)

// ErrVideoTabEpisodeMissing stops host and device fallback for a response that
// does not contain the requested episode's nonempty video_model string.
var ErrVideoTabEpisodeMissing = errors.New("hgapi[video_tab]: requested episode model is missing")

// VideoTabQuery preserves the 22-pair template at 0x101126958 in App 1.2.31.
// Its fixed profile at 0x1019d7660 intentionally differs from the User-Agent.
// SignVideoTab replaces the final ticket while selecting signature branch 12.
func VideoTabQuery(p *DeviceProfile, id string, ticket int64) [][2]string {
	if p == nil {
		p = &DeviceProfile{}
	}
	return [][2]string{
		{"aid", "8662"},
		{"app_name", "novelread"},
		{"version_code", "74332"},
		{"version_name", "7.4.3.32"},
		{"channel", "vivo_8662_64"},
		{"device_platform", "android"},
		{"device_type", "PGT-AN10"},
		{"os_version", "12"},
		{"update_version_code", "74332"},
		{"iid", p.InstallID},
		{"cdid", p.CDID},
		{"device_id", p.DeviceID},
		{"video_id", id},
		{"video_id_type", "0"},
		{"device_level", "4"},
		{"need_all_video_definition", "1"},
		{"need_mp4_align", "0"},
		{"use_os_player", "0"},
		{"video_platform", "1024"},
		{"source", "0"},
		{"from_video_id", ""},
		{"_rticket", strconv.FormatInt(ticket, 10)},
	}
}

// VideoTabRequest constructs the signed GET without borrowing account cookies
// or tokens. The selected origin must remain fixed after signing.
func VideoTabRequest(p *DeviceProfile, id, origin string, ticket int64) (*Request, error) {
	if p == nil || !validID(p.DeviceID) || !validID(p.InstallID) || !validID(id) || ticket < 0 {
		return nil, ErrInvalidOptions
	}
	if origin == "" {
		origin = VideoTabOrigin
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrInvalidOptions
	}
	query, headers, err := sign.SignVideoTab(VideoTabQuery(p, id, ticket), ticket, p.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("%w: video_tab signature unavailable", ErrSignerUnavailable)
	}
	headers["User-Agent"] = VideoTabUserAgent
	headers["Accept"] = "application/json"
	return &Request{Method: http.MethodGet, URL: strings.TrimRight(origin, "/") + VideoTabPath + "?" + query,
		Headers: headers, SkipAuth: true, CredentialsManaged: true}, nil
}

func parseVideoTabAt(raw []byte, id, date string, now time.Time) (*model.Playback, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, ErrAnonymousLimited
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, fmt.Errorf("hgapi[video_tab]: response is not JSON")
	}
	if envelope.Code != 0 {
		return nil, &CodeError{Code: envelope.Code, Name: protocol.CodeName(envelope.Code),
			Message: envelope.Message, Kind: protocol.Classify(envelope.Code, envelope.Message), Path: VideoTabPath}
	}
	var entries map[string]json.RawMessage
	if json.Unmarshal(envelope.Data, &entries) != nil {
		return nil, ErrAnonymousLimited
	}
	for _, key := range []string{"video_model_data", "1004"} {
		if value, ok := entries[key]; ok {
			var nested map[string]json.RawMessage
			if json.Unmarshal(value, &nested) == nil {
				entries = nested
				break
			}
		}
	}
	var entry struct {
		VideoModel string `json:"video_model"`
	}
	if json.Unmarshal(entries[id], &entry) != nil || entry.VideoModel == "" {
		return nil, ErrVideoTabEpisodeMissing
	}
	var meta struct {
		Expire   int64           `json:"url_expire"`
		Fallback json.RawMessage `json:"fallback_api"`
		KeySeed  string          `json:"key_seed"`
	}
	if json.Unmarshal([]byte(entry.VideoModel), &meta) != nil {
		return nil, fmt.Errorf("hgapi[video_tab]: model is not JSON")
	}
	fallback := ""
	if json.Unmarshal(meta.Fallback, &fallback) != nil {
		var object struct {
			URL string `json:"fallback_api"`
		}
		_ = json.Unmarshal(meta.Fallback, &object)
		fallback = object.URL
	}
	wrapped, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"video_model": entry.VideoModel}})
	pb, err := protocol.ParsePlayback(wrapped, id)
	if err != nil && fallback == "" {
		return nil, fmt.Errorf("hgapi[video_tab]: model has no playable stream")
	}
	if pb == nil {
		pb = &model.Playback{EpisodeID: id}
	}
	pb.FallbackAPI, pb.KeySeed = fallback, meta.KeySeed
	pb.ExpiresAt = meta.Expire
	if pb.ExpiresAt > 0 && date != "" {
		if server, err := http.ParseTime(date); err == nil {
			if skew := now.Sub(server); skew <= -time.Minute || skew >= time.Minute {
				pb.ExpiresAt += int64(skew / time.Second)
			}
		}
	}
	if pb.ExpiresAt <= 0 {
		pb.ExpiresAt = now.Add(videoTabPreferTTL).Unix()
	}
	return pb, nil
}

// ResolveVideoTab uses existing playback identities without registering a new
// recovery device or changing catalogue/account state. Profile and Origin pin
// explicit caller choices. Protected models follow ResolvePlayback's contract.
func (c *Client) ResolveVideoTab(ctx context.Context, id string, opts PlaybackOptions) (*model.Playback, error) {
	pb, err := c.resolveVideoTab(ctx, id, opts)
	if err == nil && pb.Protected {
		return pb, ErrDRMProtected
	}
	return pb, err
}

func (c *Client) resolveVideoTab(ctx context.Context, id string, opts PlaybackOptions) (*model.Playback, error) {
	if !validID(id) {
		return nil, ErrInvalidOptions
	}
	ctx, cancel := context.WithTimeout(ctx, videoTabTimeout)
	defer cancel()
	candidates, err := c.videoTabCandidates(ctx, opts)
	if err != nil {
		return nil, err
	}
	last := error(ErrAnonymousLimited)
	for _, p := range candidates {
		pb, err := c.videoTabWith(ctx, p, id, c.videoTabOrigins(opts))
		if err == nil {
			copy := *p
			c.playMu.Lock()
			c.tabDevice = &copy
			c.playMu.Unlock()
			return pb, nil
		}
		last = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !videoTabDeviceError(err) {
			return nil, err
		}
	}
	return nil, last
}

func (c *Client) videoTabWith(ctx context.Context, p *DeviceProfile, id string, origins []string) (*model.Playback, error) {
	last := error(ErrAnonymousLimited)
	for _, origin := range origins {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		request, err := VideoTabRequest(p, id, origin, c.clock().UnixMilli())
		if err != nil {
			return nil, err
		}
		pb, err := c.doVideoTab(ctx, request, id)
		if err == nil {
			pb, err = c.compatiblePlayback(ctx, pb)
		}
		if err == nil {
			return pb, nil
		}
		last = err
		// An empty response or a device/server failure belongs to this host.
		// Try the other configured origin before replacing the identity. Only
		// an explicit terminal response or mismatched episode stops fallback.
		if playbackRequestTerminal(err) || errors.Is(err, ErrVideoTabEpisodeMissing) {
			return nil, err
		}
	}
	return nil, last
}

func (c *Client) doVideoTab(ctx context.Context, request *Request, id string) (*model.Playback, error) {
	response, err := c.transport.Do(upstream.WithFixedHost(ctx), request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &transportError{cause: err}
	}
	if response == nil {
		return nil, ErrTransport
	}
	if response.StatusCode != http.StatusOK {
		return nil, &APIError{HTTPStatus: response.StatusCode}
	}
	raw, err := responseBytes(response)
	if err != nil {
		return nil, err
	}
	date := ""
	for key, value := range response.Headers {
		if strings.EqualFold(key, "Date") {
			date = value
			break
		}
	}
	return parseVideoTabAt(raw, id, date, c.clock())
}

func videoTabDeviceError(err error) bool {
	if err == nil || playbackRequestTerminal(err) {
		return false
	}
	var code *CodeError
	if errors.As(err, &code) {
		return code.Code == 110001
	}
	return !errors.Is(err, ErrVideoTabEpisodeMissing) && !errors.Is(err, ErrInvalidOptions) && !errors.Is(err, ErrSignerUnavailable)
}

func (c *Client) videoTabCandidates(ctx context.Context, opts PlaybackOptions) ([]*DeviceProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Profile != nil {
		copy := *opts.Profile
		return []*DeviceProfile{&copy}, nil
	}
	c.playMu.Lock()
	var candidates []*DeviceProfile
	if c.tabDevice != nil && validID(c.tabDevice.DeviceID) && validID(c.tabDevice.InstallID) {
		copy := *c.tabDevice
		candidates = append(candidates, &copy)
	}
	if c.playDevice != nil && validID(c.playDevice.DeviceID) && validID(c.playDevice.InstallID) {
		copy := *c.playDevice
		candidates = append(candidates, &copy)
	}
	c.playMu.Unlock()
	p, err := c.videoTabIdentity(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && len(candidates) == 0 {
		return nil, err
	}
	if err == nil {
		candidates = append(candidates, p)
	}
	seen := make(map[string]bool, len(candidates))
	result := make([]*DeviceProfile, 0, len(candidates))
	for _, p := range candidates {
		if p == nil || !validID(p.DeviceID) || !validID(p.InstallID) || seen[p.DeviceID] {
			continue
		}
		seen[p.DeviceID] = true
		copy := *p
		result = append(result, &copy)
	}
	return result, nil
}

// videoTabIdentity only reads an imported playback profile or the existing
// catalogue profile. Unlike ensureGuest/playbackIdentity it never registers or
// saves an identity when the profile store is empty.
func (c *Client) videoTabIdentity(ctx context.Context) (*DeviceProfile, error) {
	path := c.playbackDeviceFile
	if env := os.Getenv("HG_PLAYBACK_DEVICE_FILE"); env != "" {
		path = env
	}
	if path != "" {
		raw, err := os.ReadFile(path)
		if err == nil {
			var p DeviceProfile
			var aliases struct {
				DeviceID  string `json:"device_id"`
				InstallID string `json:"install_id"`
			}
			if json.Unmarshal(raw, &p) != nil || json.Unmarshal(raw, &aliases) != nil {
				return nil, fmt.Errorf("hgapi[video_tab]: invalid playback device file")
			}
			if p.DeviceID == "" {
				p.DeviceID = aliases.DeviceID
			}
			if p.InstallID == "" {
				p.InstallID = aliases.InstallID
			}
			if !validID(p.DeviceID) || !validID(p.InstallID) {
				return nil, fmt.Errorf("hgapi[video_tab]: playback device file lacks identity")
			}
			p.Isolated = true
			return &p, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("hgapi[video_tab]: cannot read playback device file")
		}
	}
	return c.profileStore.Load(ctx)
}

func (c *Client) videoTabOrigins(opts PlaybackOptions) []string {
	if opts.Origin != "" {
		return []string{opts.Origin}
	}
	u, err := url.Parse(c.apiOrigin)
	if err != nil || u == nil {
		return []string{c.apiOrigin}
	}
	// Nonstandard hosts (including local fixtures) are explicit overrides in
	// the reference. Membership comes from its 15-host table at 0x1019db780.
	switch u.Host {
	case "api5-normal-lq.fqnovel.com", "api5-normal-sinfonlinea.fqnovel.com",
		"api5-normal-sinfonlinec.fqnovel.com", "api5-normal-sinfonlineb.fqnovel.com",
		"api5-normal-lf.fqnovel.com", "api5-normal-hl.fqnovel.com", "api.fqnovel.com",
		"api-lq.fqnovel.com", "api-hl.fqnovel.com", "api5-normal.fqnovel.com",
		"api3-normal.fqnovel.com", "api3-normal-lf.fqnovel.com", "api3-normal-lq.fqnovel.com",
		"api3-normal-hl.fqnovel.com", "api3-normal-sinfonlinea.fqnovel.com":
	default:
		return []string{c.apiOrigin}
	}
	if c.apiOrigin == VideoTabOrigin {
		return []string{VideoTabOrigin, "https://" + upstream.DefaultPlaybackHosts[0]}
	}
	return []string{VideoTabOrigin, c.apiOrigin}
}

// VideoTabModels is the exported diagnostic entry for the same route.
func (c *Client) VideoTabModels(ctx context.Context, ids []string, opts PlaybackOptions) (map[string]*model.Playback, error) {
	return c.videoTabModels(ctx, ids, opts)
}

// videoTabModels resolves at most 15 episodes with three workers. As in the
// reference route, a nonempty partial result succeeds; when every episode fails,
// the first error in input order is returned. Caller cancellation is propagated.
func (c *Client) videoTabModels(ctx context.Context, ids []string, opts PlaybackOptions) (map[string]*model.Playback, error) {
	if len(ids) > PlaybackBatchSize {
		return nil, ErrInvalidOptions
	}
	for _, id := range ids {
		if !validID(id) {
			return nil, ErrInvalidOptions
		}
	}
	out := make(map[string]*model.Playback, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		index int
		pb    *model.Playback
		err   error
	}
	jobs := make(chan int, len(ids))
	results := make(chan result, len(ids))
	for i := range ids {
		jobs <- i
	}
	close(jobs)
	for i := 0; i < min(3, len(ids)); i++ {
		go func() {
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				pb, err := c.resolveVideoTab(ctx, ids[index], opts)
				results <- result{index: index, pb: pb, err: err}
			}
		}()
	}
	failures := make([]error, len(ids))
	for range ids {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r := <-results:
			if r.err == nil {
				out[ids[r.index]] = r.pb
			} else {
				failures[r.index] = r.err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		return out, nil
	}
	return nil, failures[0]
}
