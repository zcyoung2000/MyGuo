package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"myguo/internal/hgapi"
)

type CredentialTransport interface {
	SetCredentials(cookies, token, storeRegion string)
	SetHTTPClient(client *http.Client)
}

// Transport applies the saved account to ordinary Hongguo requests. Dedicated
// playback sessions supply their own bound credentials or explicitly omit them.
type Transport struct {
	mu          sync.RWMutex
	client      *http.Client
	cookies     string
	token       string
	storeRegion string
}

func New() *Transport {
	return &Transport{client: &http.Client{Timeout: 30 * time.Second}}
}

func (t *Transport) SetHTTPClient(client *http.Client) {
	if client == nil {
		return
	}
	t.mu.Lock()
	t.client = client
	t.mu.Unlock()
}

func (t *Transport) SetCredentials(cookies, token, storeRegion string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cookies = strings.TrimSpace(cookies)
	t.token = strings.TrimSpace(token)
	t.storeRegion = strings.TrimSpace(storeRegion)
}

func (t *Transport) Do(ctx context.Context, req *hgapi.Request) (*hgapi.Response, error) {
	if req == nil {
		return nil, errors.New("auth transport: nil request")
	}
	copy := *req
	copy.Headers = make(map[string]string, len(req.Headers)+3)
	for key, value := range req.Headers {
		copy.Headers[key] = value
	}
	cookies, token, region := t.credentials()
	if !req.SkipAuth && !req.CredentialsManaged && cookies != "" && !hgapi.IsDeviceRegistration(req.URL) {
		copy.Headers["Cookie"], copy.Headers["x-tt-token"], copy.Headers["x-tt-store-region"] = cookies, token, region
	}
	t.mu.RLock()
	client := t.client
	t.mu.RUnlock()
	return hgapi.Exchange(ctx, client, &copy)
}

func (t *Transport) credentials() (cookies, token, storeRegion string) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cookies, t.token, t.storeRegion
}

// Credentials snapshots the current account for a separately bound playback
// session. The snapshot is never logged.
func (t *Transport) Credentials() (cookies, token, storeRegion string) { return t.credentials() }
