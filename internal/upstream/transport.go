package upstream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
)

const DefaultMaxHostAttempts = 3
const maxReplayBodyBytes = 8 << 20

type fixedHostKey struct{}

// WithFixedHost prevents the generic host transport from moving a request
// whose signature was bound to an explicitly selected playback host.
func WithFixedHost(ctx context.Context) context.Context {
	return context.WithValue(ctx, fixedHostKey{}, true)
}

type Transport struct {
	mu          sync.RWMutex
	picker      *HostPicker
	next        http.RoundTripper
	enabled     bool
	maxAttempts int
}

func NewTransport(picker *HostPicker, next http.RoundTripper) *Transport {
	if next == nil {
		next = http.DefaultTransport
	}
	return &Transport{
		picker:      picker,
		next:        next,
		enabled:     true,
		maxAttempts: DefaultMaxHostAttempts,
	}
}

func (t *Transport) SetEnabled(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = enabled
}

func (t *Transport) Enabled() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.enabled
}

func (t *Transport) SetMaxAttempts(value int) {
	if value > 0 {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.maxAttempts = value
	}
}

func (t *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.RLock()
	enabled, picker, next, maxAttempts := t.enabled, t.picker, t.next, t.maxAttempts
	t.mu.RUnlock()
	if !enabled || picker == nil || request.Context().Value(fixedHostKey{}) == true {
		return next.RoundTrip(request)
	}
	family := FamilyForPath(request.URL.Path)
	hosts := picker.Order(family)
	if len(hosts) == 0 || !containsHost(hosts, request.URL.Host) {
		return next.RoundTrip(request)
	}

	attempts := min(maxAttempts, len(hosts))
	var lastResponse *http.Response
	var lastErr error
	var payload []byte
	if request.Body != nil {
		var err error
		payload, err = io.ReadAll(io.LimitReader(request.Body, maxReplayBodyBytes+1))
		_ = request.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(payload) > maxReplayBodyBytes {
			return nil, errors.New("request body exceeds host retry limit")
		}
	}
	for index := 0; index < attempts; index++ {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		attempt, err := t.requestFor(request, hosts[index], payload)
		if err != nil {
			return nil, err
		}
		response, roundTripErr := next.RoundTrip(attempt)
		host := attempt.URL.Host
		if !hostFailure(response, roundTripErr) {
			if roundTripErr != nil {
				return nil, roundTripErr
			}
			t.picker.MarkGood(family, host)
			return response, nil
		}
		t.picker.MarkBad(family, host)
		// The caller owns the final response, including an HTTP error body.
		if index == attempts-1 {
			return response, roundTripErr
		}
		drainAndClose(response)
		lastResponse, lastErr = response, roundTripErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return lastResponse, nil
}

func (t *Transport) requestFor(request *http.Request, host string, body []byte) (*http.Request, error) {
	attempt := request.Clone(request.Context())
	attempt.URL.Host = host
	attempt.Host = ""
	if body == nil {
		attempt.Body = nil
		return attempt, nil
	}
	attempt.Body = io.NopCloser(bytes.NewReader(body))
	attempt.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return attempt, nil
}

func hostFailure(response *http.Response, err error) bool {
	if err != nil || response == nil {
		return true
	}
	if response.StatusCode >= http.StatusInternalServerError {
		return true
	}
	chunk := make([]byte, 1)
	read, readErr := response.Body.Read(chunk)
	if readErr != nil && readErr != io.EOF {
		return true
	}
	if read == 0 {
		return true
	}
	response.Body = newPrefixedBody(chunk[:read], response.Body)
	return false
}

type prefixedBody struct {
	reader io.Reader
	body   io.ReadCloser
}

func newPrefixedBody(prefix []byte, body io.ReadCloser) io.ReadCloser {
	return &prefixedBody{reader: io.MultiReader(bytes.NewReader(prefix), body), body: body}
}

func (body *prefixedBody) Read(buffer []byte) (int, error) {
	return body.reader.Read(buffer)
}

func (body *prefixedBody) Close() error {
	return body.body.Close()
}

func drainAndClose(response *http.Response) {
	if response == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
}
