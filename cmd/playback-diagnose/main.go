// playback-diagnose checks the real SDK playback path without logging account
// credentials, device identifiers, signed media URLs, or key material.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/beilunyang/openhongguo/adapters/memstore"
	"github.com/beilunyang/openhongguo/sdk"
	_ "modernc.org/sqlite"
	"myguo/internal/auth"
	"myguo/internal/upstream"
)

type inspect struct{ inner sdk.Transport }

func (t inspect) Do(ctx context.Context, request *sdk.Request) (*sdk.Response, error) {
	response, err := t.inner.Do(ctx, request)
	target, _ := url.Parse(request.URL)
	if response != nil {
		var envelope struct {
			Code json.RawMessage `json:"code"`
		}
		_ = json.Unmarshal(response.Body, &envelope)
		fmt.Printf("%s %s status=%d bytes=%d code=%s\n", request.Method, target.Path, response.StatusCode, len(response.Body), envelope.Code)
	}
	return response, err
}
func run() error {
	episode := flag.String("episode", "7694371347610209304", "episode to resolve")
	accountDB := flag.String("account-db", "", "optional account database, opened read-only; device state remains in memory")
	flag.Parse()
	transport := auth.New()
	if *accountDB != "" {
		location := (&url.URL{Scheme: "file", Path: *accountDB, RawQuery: "mode=ro"}).String()
		db, err := sql.Open("sqlite", location)
		if err != nil {
			return err
		}
		defer db.Close()
		var cookies, token string
		if err := db.QueryRow("SELECT cookies, token FROM account_auth WHERE id = 1").Scan(&cookies, &token); err != nil {
			return err
		}
		transport.SetCredentials(cookies, token, "cn")
	}
	client, err := sdk.NewClient(sdk.Options{Transport: inspect{transport}, ProfileStore: memstore.New(), APIOrigin: "https://" + upstream.DefaultPlaybackHosts[0], RetryAttempts: 1})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	playback, err := client.ResolvePlayback(ctx, *episode, sdk.PlaybackOptions{})
	if playback == nil {
		return err
	}
	if playback.Protected {
		if _, err := sdk.ContentKey(playback); err != nil {
			return err
		}
	}
	fmt.Printf("streams=%d duration=%ds protected=%t content_key_valid=true\n", len(playback.Streams), playback.DurationSeconds, playback.Protected)
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Playback diagnosis failed:", err)
		os.Exit(1)
	}
}
