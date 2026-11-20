// Package httptransport implements the app-owned Hongguo transport.
package httptransport

import (
	"context"
	"net/http"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/protocol"
)

type Transport struct {
	client *http.Client
	logger hgapi.Logger
}

func New() *Transport                              { return &Transport{client: &http.Client{Timeout: 30 * time.Second}} }
func (t *Transport) SetLogger(logger hgapi.Logger) { t.logger = logger }
func (t *Transport) Do(ctx context.Context, req *hgapi.Request) (*hgapi.Response, error) {
	response, err := hgapi.Exchange(ctx, t.client, req)
	if err == nil && t.logger != nil {
		t.logger.Printf("HTTP %s %s -> %d", req.Method, protocol.RedactedURL(req.URL), response.StatusCode)
	}
	return response, err
}
