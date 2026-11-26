// Adapted from openhongguo (Apache-2.0), commit 1d3354a932bc.
// Modified for myguo: local data/catalog compatibility; transport and playback are app-owned.
package protocol

import (
	"net/url"
)

// RedactedURL keeps scheme, host, and path. Query strings are dropped so
// device ids, tickets, and playback parameters never reach a log line.
func RedactedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "[invalid URL]"
	}
	return u.Scheme + "://" + u.Host + u.Path
}
