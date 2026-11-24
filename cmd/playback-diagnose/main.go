// playback-diagnose checks the real SDK playback path without logging account
// credentials, device identifiers, signed media URLs, or key material.
package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"myguo/internal/auth"
	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters/httptransport"
	"myguo/internal/hgapi/adapters/memstore"
	"myguo/internal/hgapi/model"
	"myguo/internal/media"
	"myguo/internal/sign"
	"myguo/internal/sqlitedsn"
	"myguo/internal/upstream"
)

type inspect struct{ inner hgapi.Transport }

func (t inspect) Do(ctx context.Context, request *hgapi.Request) (*hgapi.Response, error) {
	response, err := t.inner.Do(ctx, request)
	target, _ := url.Parse(request.URL)
	if response != nil {
		var envelope struct {
			Code json.RawMessage `json:"code"`
		}
		_ = json.Unmarshal(response.Body, &envelope)
		fmt.Printf("%s %s status=%d bytes=%d code=%s\n", request.Method, target.Path, response.StatusCode, len(response.Body), envelope.Code)
		for _, name := range []string{"Server", "Date", "Content-Type", "Content-Length", "Content-Encoding"} {
			if value := response.Headers[name]; value != "" {
				fmt.Printf("response %s=%q\n", name, value)
			}
		}
	}
	return response, err
}
func run() error {
	episode := flag.String("episode", "7694371347610209304", "episode to resolve")
	series := flag.String("series", "", "compare episode-list and batch-detail endpoints for a series")
	accountDB := flag.String("account-db", "", "optional account database, opened read-only; device state remains in memory")
	deviceDB := flag.String("device-db", "", "optional installed device database, opened read-only; recovery remains in memory")
	origin := flag.String("origin", "https://"+upstream.DefaultPlaybackHosts[0], "upstream origin to compare")
	http1 := flag.Bool("http1", false, "compare HTTP/1.1 with the default HTTP/2 transport")
	checkPlayback := flag.Bool("check-playback", false, "resolve the first episode after comparing series endpoints")
	noReset := flag.Bool("no-device-reset", false, "disable device replacement during a diagnostic request")
	builtin := flag.Bool("sdk-transport", false, "use the unmodified SDK anonymous HTTP transport")
	wireTrace := flag.Bool("wire-trace", false, "report HTTP protocol, signature presence and server clock skew without secrets")
	tabOnly := flag.Bool("video-tab-only", false, "compare the reference 1.2.31 GET route without the POST route")
	sessionOnly := flag.Bool("session-only", false, "verify the SDI session-signing channel without legacy fallback")
	legacyOnly := flag.Bool("legacy-only", false, "compare legacy playback without the SDI channel")
	modeName := flag.String("mode", "auto", "playback request mode: auto, full, minimal, helios or none")
	sampleOut := flag.String("sample-out", "", "download a decrypted copy through the production media server")
	flag.Parse()
	transport := auth.New()
	if *http1 || *wireTrace {
		httpTransport := http.DefaultTransport.(*http.Transport).Clone()
		if *http1 {
			httpTransport.Protocols = new(http.Protocols)
			httpTransport.Protocols.SetHTTP1(true)
			httpTransport.ForceAttemptHTTP2 = false
			httpTransport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
			httpTransport.TLSClientConfig = &tls.Config{NextProtos: []string{"http/1.1"}}
		}
		var roundTripper http.RoundTripper = httpTransport
		if *wireTrace {
			roundTripper = wireInspector{httpTransport}
		}
		transport.SetHTTPClient(&http.Client{Timeout: 30 * time.Second, Transport: roundTripper})
	}
	profile := memstore.New()
	if *deviceDB != "" {
		p, err := readDevice(*deviceDB)
		if err != nil {
			return err
		}
		if err := profile.Save(context.Background(), p); err != nil {
			return err
		}
	}
	if *accountDB != "" {
		accountURI := sqlitedsn.FileURL(*accountDB)
		accountURI.RawQuery = "mode=ro"
		location := accountURI.String()
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
	var sender hgapi.Transport = transport
	if *builtin {
		if *accountDB != "" || *wireTrace || *http1 {
			return fmt.Errorf("-sdk-transport compares anonymous default transport; omit -account-db, -wire-trace and -http1")
		}
		sender = httptransport.New()
	}
	options := hgapi.Options{Transport: inspect{sender}, ProfileStore: profile, APIOrigin: *origin, RetryAttempts: 1}
	if *legacyOnly {
		disabled := false
		options.SessionPlayback = &disabled
	}
	if *sessionOnly && (*legacyOnly || *tabOnly || *modeName != "auto") {
		return fmt.Errorf("-session-only requires auto mode without legacy/video-tab overrides")
	}
	if *noReset {
		zero := 0
		options.DeviceResets = &zero
	}
	client, err := hgapi.NewClient(options)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if *series != "" {
		page, listErr := client.ListEpisodes(ctx, *series, hgapi.EpisodeOptions{})
		count := 0
		if page != nil {
			count = len(page.Items)
		}
		fmt.Printf("single episodes=%d error=%v\n", count, listErr)
		var first *model.Episode
		if page != nil {
			for _, ep := range page.Items {
				if ep.Number == 1 {
					copy := ep
					first = &copy
					fmt.Printf("single first episode=%s series_matches=%t playable=%t\n", ep.ID, ep.SeriesID == *series, ep.Playable)
					break
				}
			}
		}
		details, batchErr := client.GetSeriesBatch(ctx, []string{*series})
		fmt.Printf("batch series=%d error=%v\n", len(details), batchErr)
		for _, d := range details {
			if d.Series != nil {
				fmt.Printf("batch title=%q episodes=%d declared=%d status=%v rightText=%q\n", d.Series.Title, len(d.Episodes), d.Series.EpisodeCount, d.Series.Status, d.Series.RightText)
				for _, ep := range d.Episodes {
					if ep.Number == 1 {
						fmt.Printf("batch first episode=%s series_matches=%t single_matches=%t playable=%t\n", ep.ID, ep.SeriesID == *series, first != nil && first.ID == ep.ID, ep.Playable)
						break
					}
				}
			}
		}
		if !*checkPlayback || first == nil {
			return nil
		}
		*episode = first.ID
	}
	var playback *model.Playback
	playOptions := hgapi.PlaybackOptions{}
	if *modeName != "auto" {
		modes := map[string]sign.Mode{"full": sign.Full, "minimal": sign.Minimal, "helios": sign.Helios, "none": sign.None}
		mode, ok := modes[*modeName]
		if !ok {
			return fmt.Errorf("invalid playback request mode")
		}
		playOptions.Mode = &mode
	}
	if *sessionOnly {
		playback, err = client.ResolveSessionPlayback(ctx, *episode, playOptions)
	} else if *tabOnly {
		playback, err = client.ResolveVideoTab(ctx, *episode, playOptions)
	} else {
		playback, err = client.ResolvePlayback(ctx, *episode, playOptions)
	}
	if playback == nil {
		return err
	}
	if playback.Protected {
		if _, err := hgapi.ContentKey(playback); err != nil {
			return err
		}
	}
	fmt.Printf("streams=%d duration=%ds protected=%t content_key_valid=true\n", len(playback.Streams), playback.DurationSeconds, playback.Protected)
	if *sampleOut != "" {
		root, err := os.MkdirTemp("", "myguo-live-media-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(root)
		_ = os.Setenv("MYGUO_DATA_ROOT", root)
		server, err := media.NewMediaServer(client)
		if err != nil {
			return err
		}
		defer server.Close()
		server.RememberPlayback(*episode, playback)
		request, err := http.NewRequestWithContext(context.Background(), "GET", server.VideoURL(*episode, "7703599011814460478", 1, "720p"), nil)
		if err != nil {
			return err
		}
		response, err := (&http.Client{Timeout: 90 * time.Second}).Do(request)
		if err != nil {
			return fmt.Errorf("media sample download failed")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("media server returned HTTP %d", response.StatusCode)
		}
		if err = os.MkdirAll(filepath.Dir(*sampleOut), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(*sampleOut, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(file, io.LimitReader(response.Body, 256<<20))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n == 0 {
			return fmt.Errorf("empty media sample")
		}
		fmt.Printf("decrypted_media_bytes=%d sample=%s\n", n, *sampleOut)
	}
	return nil
}

func (t inspect) Credentials() (string, string, string) {
	if source, ok := t.inner.(hgapi.CredentialSource); ok {
		return source.Credentials()
	}
	return "", "", ""
}

type wireInspector struct{ next http.RoundTripper }

func (t wireInspector) RoundTrip(request *http.Request) (*http.Response, error) {
	var negotiated string
	trace := &httptrace.ClientTrace{TLSHandshakeDone: func(state tls.ConnectionState, _ error) { negotiated = state.NegotiatedProtocol }}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	response, err := t.next.RoundTrip(request)
	if strings.Contains(request.URL.Path, "video_model") {
		fmt.Printf("wire host=%s alpn=%q argus_bytes=%d ladon_bytes=%d medusa_present=%t\n", request.URL.Hostname(), negotiated,
			len(request.Header.Get("x-argus")), len(request.Header.Get("x-ladon")), request.Header.Get("x-medusa") != "")
		if response != nil {
			khronos, _ := strconv.ParseInt(request.Header.Get("x-khronos"), 10, 64)
			serverTime, dateErr := http.ParseTime(response.Header.Get("Date"))
			if dateErr == nil {
				fmt.Printf("wire protocol=%s server_clock_skew_seconds=%d\n", response.Proto, serverTime.Unix()-khronos)
			}
		}
	}
	if err != nil {
		// Do not print a URL-bearing transport error's signed query.
		fmt.Printf("wire transport_error=true eof=%t\n", errors.Is(err, io.EOF))
	}
	return response, err
}

func readDevice(path string) (*hgapi.DeviceProfile, error) {
	deviceURI := sqlitedsn.FileURL(path)
	deviceURI.RawQuery = "mode=ro"
	location := deviceURI.String()
	db, err := sql.Open("sqlite", location)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var p hgapi.DeviceProfile
	var d model.Device
	var created int64
	var pad int
	err = db.QueryRow(`SELECT cdid, openudid, clientudid, oaid, reqid,
		device_id, install_id, device_token, ttreq, store_region, store_region_src, created_at,
		device_brand, device_model, device_os_version, device_os_api,
		device_build, device_resolution, device_dpi, device_pad, device_channel
		FROM device_profiles WHERE id = 1`).Scan(
		&p.CDID, &p.OpenUDID, &p.ClientUDID, &p.OAID, &p.ReqID,
		&p.DeviceID, &p.InstallID, &p.DeviceToken, &p.TTReq, &p.StoreRegion, &p.StoreRegionSrc, &created,
		&d.Brand, &d.Model, &d.OSVersion, &d.OSAPI, &d.Build, &d.Resolution, &d.DPI, &pad, &d.Channel)
	if err != nil {
		return nil, err
	}
	p.CreatedAt = time.Unix(created, 0).UTC()
	d.Pad = pad != 0
	p.Device = &d
	return &p, nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Playback diagnosis failed:", err)
		os.Exit(1)
	}
}
