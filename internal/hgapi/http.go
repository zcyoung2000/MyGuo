package hgapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Exchange is the single HTTP boundary for API transports. It applies no
// account, signing or retry policy; those belong to the owning API session.
// Cookies are returned separately so credentials cannot enter ordinary logs.
func Exchange(ctx context.Context, client *http.Client, request *Request) (*Response, error) {
	if request == nil {
		return nil, fmt.Errorf("nil protocol request")
	}
	if client == nil {
		return nil, fmt.Errorf("nil HTTP transport")
	}
	method := request.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, request.URL, bytes.NewReader(request.Body))
	if err != nil {
		return nil, err
	}
	for key, value := range request.Headers {
		req.Header.Set(key, value)
	}
	if request.SkipAuth {
		for _, key := range []string{"Cookie", "Authorization", "X-Tt-Token"} {
			req.Header.Del(key)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &transportError{cause: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > responseLimit {
		return nil, fmt.Errorf("%w: response too large", ErrTransport)
	}
	headers := make(map[string]string, len(resp.Header))
	for key, values := range resp.Header {
		if len(values) > 0 && !strings.EqualFold(key, "Set-Cookie") && !strings.EqualFold(key, "Cookie") {
			headers[key] = values[0]
		}
	}
	cookies := make(map[string]string)
	for _, cookie := range resp.Cookies() {
		cookies[cookie.Name] = cookie.Value
	}
	return &Response{StatusCode: resp.StatusCode, Headers: headers, Body: body, Cookies: cookies}, nil
}

// IsDeviceRegistration identifies the protocol call that must not inherit an account.
func IsDeviceRegistration(rawURL string) bool {
	return strings.Contains(rawURL, "/service/2/device_register/")
}
