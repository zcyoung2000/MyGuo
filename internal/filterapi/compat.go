// Package filterapi fills the pinned SDK's missing comic art-style selector.
// Device registration, pagination, response parsing and retries remain in the
// SDK; only this official field is added before signing the outgoing body.
package filterapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/beilunyang/openhongguo/sdk"
	"github.com/beilunyang/openhongguo/sdk/model"
	"myguo/internal/sign"
)

const artStyle = "category_dim_art_style"
const cursorPrefix = "art1."

type extensionKey struct{}
type extension struct {
	values  []string
	applied atomic.Bool
}
type cursor struct {
	SDK   string   `json:"cursor"`
	Style []string `json:"style"`
}

func Query(ctx context.Context, client *sdk.Client, selections map[string][]string, token string) (*model.ShortFilterPage, error) {
	style := selections[artStyle]
	if strings.HasPrefix(token, cursorPrefix) {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, cursorPrefix))
		var saved cursor
		if err != nil || json.Unmarshal(raw, &saved) != nil || saved.SDK == "" || strings.Join(saved.Style, "\x00") != strings.Join(style, "\x00") {
			return nil, fmt.Errorf("cursor belongs to a different art-style filter")
		}
		token = saved.SDK
	} else if token != "" && len(style) > 0 {
		return nil, fmt.Errorf("cursor does not belong to this art-style filter")
	}
	if len(style) == 0 {
		return client.ShortFilter(ctx, sdk.ShortFilterQuery{Selections: selections, Cursor: token})
	}
	query := make(map[string][]string, len(selections))
	for key, values := range selections {
		if key != artStyle {
			query[key] = values
		}
	}
	for _, value := range style {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("empty art-style selector")
		}
	}
	extra := &extension{values: append([]string(nil), style...)}
	page, err := client.ShortFilter(context.WithValue(ctx, extensionKey{}, extra), sdk.ShortFilterQuery{Selections: query, Cursor: token})
	if err != nil {
		return nil, err
	}
	if !extra.applied.Load() {
		return nil, fmt.Errorf("transport does not support the art-style selector")
	}
	if page != nil && page.Cursor != "" {
		copy := *page
		raw, _ := json.Marshal(cursor{SDK: page.Cursor, Style: style})
		copy.Cursor = cursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
		page = &copy
	}
	return page, nil
}

// AmendSignedRequest runs before the account transport sends an SDK request.
// Re-sign the exact modified bytes, preserving the ordered device query and
// guest headers. Requests without an explicit extension are returned untouched.
func AmendSignedRequest(ctx context.Context, req *sdk.Request) (*sdk.Request, error) {
	extra, _ := ctx.Value(extensionKey{}).(*extension)
	if extra == nil || req == nil {
		return req, nil
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
	}
	if u.Path != "/reading/distribution/category/landpage/v1/" {
		return req, nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return nil, err
	}
	var kind string
	_ = json.Unmarshal(body["req_type"], &kind)
	if kind != "only_content" {
		return req, nil
	}
	var selections map[string][]string
	if err := json.Unmarshal(body["select_items"], &selections); err != nil || selections == nil {
		return nil, fmt.Errorf("missing filter selections")
	}
	selections[artStyle] = extra.values
	body["select_items"], _ = json.Marshal(selections)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var pairs [][2]string
	for _, pair := range strings.Split(u.RawQuery, "&") {
		key, value, _ := strings.Cut(pair, "=")
		key, err = url.QueryUnescape(key)
		if err != nil {
			return nil, err
		}
		value, err = url.QueryUnescape(value)
		if err != nil {
			return nil, err
		}
		if key != "ts" && key != "_rticket" {
			pairs = append(pairs, [2]string{key, value})
		}
	}
	identity := u.Query()
	query, headers := sign.SignPOST(u.Path, pairs, time.Now().UnixMilli(), identity.Get("device_id"), identity.Get("version_name"), payload, nil)
	u.RawQuery = query
	copy := *req
	copy.URL, copy.Body = u.String(), payload
	copy.Headers = make(map[string]string, len(req.Headers)+len(headers))
	for key, value := range req.Headers {
		copy.Headers[key] = value
	}
	for key, value := range headers {
		copy.Headers[key] = value
	}
	extra.applied.Store(true)
	return &copy, nil
}
