// Package sqlitedsn builds SQLite file URIs that work on every platform.
package sqlitedsn

import (
	"net/url"
	"path/filepath"
	"strings"
)

// FileURL returns a file:// URL for path.
//
// On Windows an absolute path starts with a volume name such as "C:". Passing
// that path as url.URL.Path makes url.URL.String() render "file://C:/...",
// where "C:" is parsed as the URI authority. modernc.org/sqlite rejects it
// with "invalid uri authority". A leading slash yields the valid
// "file:///C:/..." form instead.
func FileURL(path string) *url.URL {
	p := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return &url.URL{Scheme: "file", Path: p}
}
