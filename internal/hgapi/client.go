// Package hgapi owns myguo's Hongguo wire protocol and playback sessions.
package hgapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"myguo/internal/hgapi/model"
	"myguo/internal/hgapi/protocol"
)

const responseLimit = 8 << 20

type Logger interface{ Printf(string, ...any) }

type Options struct {
	Transport                                     Transport
	ProfileStore                                  ProfileStore
	Logger                                        Logger
	Clock                                         func() time.Time
	APIOrigin, RegisterOrigin, DistributionOrigin string
	Device                                        model.Device
	RetryAttempts                                 int
	DeviceResets                                  *int
	PlaybackDeviceFile                            string
}

func Int(v int) *int { return &v }

type Client struct {
	transport                             Transport
	profileStore                          ProfileStore
	logger                                Logger
	clock                                 func() time.Time
	apiOrigin, registerOrigin, distOrigin string
	playbackDeviceFile                    string
	device                                model.Device
	retryAttempts, deviceResets           int
	guestMu                               sync.Mutex
	resetMu                               sync.Mutex
	playMu                                sync.Mutex
	playGoodMode                          bool
	playGoodAt                            time.Time
	playCache                             map[string]*model.Playback
	playLoads                             map[string]*playbackLoad
	playDevice                            *DeviceProfile
	tabDevice                             *DeviceProfile
	preferTabUntil                        time.Time
	unsignedDevice                        *DeviceProfile
	unsignedAt                            time.Time
	playFreshMu                           sync.Mutex
}

func NewClient(opts Options) (*Client, error) {
	if opts.Transport == nil || opts.ProfileStore == nil {
		return nil, ErrInvalidOptions
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.APIOrigin == "" {
		opts.APIOrigin = "https://" + protocol.APIHost
	}
	if opts.RegisterOrigin == "" {
		opts.RegisterOrigin = "https://" + protocol.RegisterHost
	}
	if opts.DistributionOrigin == "" {
		opts.DistributionOrigin = opts.APIOrigin
	}
	for _, origin := range []string{opts.APIOrigin, opts.RegisterOrigin, opts.DistributionOrigin} {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
			return nil, ErrInvalidOptions
		}
	}
	resets := 2
	if opts.DeviceResets != nil {
		resets = max(0, *opts.DeviceResets)
	}
	return &Client{transport: opts.Transport, profileStore: opts.ProfileStore, logger: opts.Logger, clock: opts.Clock,
		apiOrigin: strings.TrimRight(opts.APIOrigin, "/"), registerOrigin: strings.TrimRight(opts.RegisterOrigin, "/"), distOrigin: strings.TrimRight(opts.DistributionOrigin, "/"),
		device: opts.Device.OrDefault(), retryAttempts: max(1, opts.RetryAttempts), deviceResets: resets,
		playbackDeviceFile: opts.PlaybackDeviceFile,
		playCache:          make(map[string]*model.Playback), playLoads: make(map[string]*playbackLoad)}, nil
}

func (c *Client) doRaw(ctx context.Context, method, rawURL string, headers map[string]string, body []byte) ([]byte, map[string]string, error) {
	return c.send(ctx, &Request{Method: method, URL: rawURL, Headers: headers, Body: body}, c.retryAttempts)
}

func (c *Client) send(ctx context.Context, request *Request, attempts int) ([]byte, map[string]string, error) {
	var last error
	for attempt := 0; attempt < max(1, attempts); attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		response, err := c.transport.Do(ctx, request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			// Transport errors frequently contain signed URLs; retain their cause
			// for errors.Is without exposing its text to UI/logs.
			last = &transportError{cause: err}
		} else {
			raw, decodeErr := readResponse(response)
			if decodeErr == nil {
				return raw, response.Cookies, nil
			}
			last = decodeErr
		}
		if !Retryable(last) || attempt+1 >= attempts {
			break
		}
		wait := RetryAfter(last, time.Duration(attempt+1)*100*time.Millisecond)
		if wait > 5*time.Second {
			break
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, nil, last
}

type transportError struct{ cause error }

func (e *transportError) Error() string        { return "hgapi: transport failure" }
func (e *transportError) Unwrap() error        { return e.cause }
func (e *transportError) Is(target error) bool { return target == ErrTransport }

func readResponse(response *Response) ([]byte, error) {
	if response == nil {
		return nil, ErrTransport
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return nil, ErrAuthenticationRequired
	}
	if response.StatusCode == 429 {
		return nil, ErrRateLimited
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &APIError{HTTPStatus: response.StatusCode}
	}
	body, err := responseBytes(response)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, ErrAnonymousLimited
	}
	if code, message, ok := protocol.BusinessCode(body); ok && code != "0" {
		n, _ := strconv.Atoi(code)
		return nil, &CodeError{Code: n, Name: protocol.CodeName(n), Message: message, Kind: protocol.Classify(n, message), HTTPStatus: response.StatusCode}
	}
	if status := protocol.BaseStatus(body); protocol.DeviceRejected(status) {
		return nil, &CodeError{Code: status, Kind: protocol.KindDeviceRejected}
	}
	return body, nil
}

func (c *Client) ensureSearchGuest(ctx context.Context) (*DeviceProfile, error) {
	return c.ensureGuest(ctx)
}
func (c *Client) ensureGuest(ctx context.Context) (*DeviceProfile, error) {
	c.guestMu.Lock()
	defer c.guestMu.Unlock()
	p, err := c.profileStore.Load(ctx)
	if err != nil && !errors.Is(err, ErrProfileNotFound) {
		return nil, err
	}
	if p != nil && p.DeviceID != "" && p.InstallID != "" {
		if p.Device == nil {
			d := c.device
			p.Device = &d
		}
		return p, nil
	}
	if p == nil {
		p, err = NewAnonymousDeviceProfile(nil, c.clock)
		if err != nil {
			return nil, err
		}
	}
	d := c.device
	p.Device = &d
	if err = c.register(ctx, p); err != nil {
		return nil, err
	}
	if err = c.profileStore.Save(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (c *Client) register(ctx context.Context, p *DeviceProfile) error {
	d := c.deviceOf(p)
	body, err := protocol.BuildRegisterBody(protocol.RegisterIdentity{CDID: p.CDID, OpenUDID: p.OpenUDID, ClientUDID: p.ClientUDID, OAID: p.OAID, ReqID: p.ReqID, Device: d}, c.clock())
	if err != nil {
		return err
	}
	q := url.Values{"req_id": {p.ReqID}, "tt_data": {"a"}}
	headers := map[string]string{"User-Agent": protocol.UserAgent(d), "Content-Type": "application/octet-stream;tt-data=a", "log-encode-type": "gzip", "sdk-version": "2", "passport-sdk-version": "5051452", "x-vc-bdturing-sdk-version": "4.0.3.cn", "x-ss-dp": protocol.AID, "X-SS-STUB": protocol.ContentMD5(body)}
	raw, cookies, err := c.send(ctx, &Request{Method: "POST", URL: c.registerOrigin + protocol.PathRegister + "?" + q.Encode(), Headers: headers, Body: body, SkipAuth: true}, 1)
	if err != nil {
		return err
	}
	registered, err := protocol.ParseRegister(raw)
	if err != nil {
		return err
	}
	p.DeviceID, p.InstallID = registered.DeviceID, registered.InstallID
	applyGuestCookies(p, cookies)
	return nil
}

func (c *Client) ResetDevice(ctx context.Context) error {
	c.resetMu.Lock()
	defer c.resetMu.Unlock()
	c.guestMu.Lock()
	defer c.guestMu.Unlock()
	p, err := NewAnonymousDeviceProfile(nil, c.clock)
	if err != nil {
		return err
	}
	d := c.device
	p.Device = &d
	if err = c.register(ctx, p); err != nil {
		return err
	}
	if err = c.profileStore.Save(ctx, p); err != nil {
		return err
	}
	c.ClearPlaybackCache()
	return nil
}

type resetContextKey struct{}

func withFreshDevice[T any](ctx context.Context, c *Client, _ bool, call func(context.Context) (T, error)) (T, error) {
	value, err := call(ctx)
	if !errors.Is(err, ErrDeviceRejected) || c.deviceResets == 0 || ctx.Value(resetContextKey{}) != nil {
		return value, err
	}
	if resetErr := c.ResetDevice(ctx); resetErr != nil {
		return value, resetErr
	}
	return call(context.WithValue(ctx, resetContextKey{}, true))
}

func applyGuestCookies(p *DeviceProfile, cookies map[string]string) {
	for name, value := range cookies {
		switch strings.ToLower(name) {
		case "ttreq":
			p.TTReq = value
		case "store-region":
			p.StoreRegion = value
		case "store-region-src":
			p.StoreRegionSrc = value
		}
	}
}
func guestCookie(p *DeviceProfile) string {
	parts := []string{"install_id=" + p.InstallID}
	if p.TTReq != "" {
		parts = append(parts, "ttreq="+p.TTReq)
	}
	if p.StoreRegion != "" {
		parts = append(parts, "store-region="+p.StoreRegion, "store-region-src=did")
	}
	return strings.Join(parts, "; ")
}
func (c *Client) deviceOf(p *DeviceProfile) model.Device {
	if p != nil && p.Device != nil {
		return p.Device.OrDefault()
	}
	return c.device
}
func (c *Client) identity(p *DeviceProfile) protocol.SearchIdentity {
	return protocol.SearchIdentity{DeviceID: p.DeviceID, InstallID: p.InstallID, CDID: p.CDID, OpenUDID: p.OpenUDID, Device: c.deviceOf(p)}
}
func (c *Client) randomInt31() int32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return int32(binary.LittleEndian.Uint32(b[:]) & 0x7fffffff)
}

func randomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return uuid(b[:])
}

func responseBytes(response *Response) ([]byte, error) {
	var reader io.Reader = bytes.NewReader(response.Body)
	for name, value := range response.Headers {
		if !strings.EqualFold(name, "Content-Encoding") || value == "" {
			continue
		}
		if strings.EqualFold(value, "gzip") {
			z, err := gzip.NewReader(reader)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid gzip", ErrTransport)
			}
			defer z.Close()
			reader = z
		} else if value != "identity" {
			return nil, fmt.Errorf("%w: unsupported response encoding", ErrTransport)
		}
	}
	body, err := io.ReadAll(io.LimitReader(reader, responseLimit+1))
	if err != nil {
		return nil, ErrTransport
	}
	if len(body) > responseLimit {
		return nil, fmt.Errorf("%w: response too large", ErrTransport)
	}

	return body, nil
}
