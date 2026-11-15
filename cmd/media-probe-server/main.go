// media-probe-server serves deterministic AVFoundation transport fixtures. The
// growing endpoint mirrors MediaServer's unknown-length 206 range contract;
// chunked tests the initial open GET; cached/HLS exercise supported fallbacks.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	directory := flag.String("dir", "", "fixture directory")
	address := flag.String("addr", "127.0.0.1:18763", "local address")
	rate := flag.Int64("rate", 256*1024, "published bytes per second")
	flag.Parse()
	if *directory == "" {
		log.Fatal("-dir is required")
	}
	fragmented := filepath.Join(*directory, "fragmented.mp4")
	info, err := os.Stat(fragmented)
	if err != nil {
		log.Fatal(err)
	}
	var mu sync.Mutex
	starts := map[string]time.Time{}
	mux := http.NewServeMux()
	mux.HandleFunc("/growing.mp4", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("run")
		mu.Lock()
		started := starts[key]
		if started.IsZero() {
			started = time.Now()
			starts[key] = started
		}
		mu.Unlock()
		published := func() int64 { return min(info.Size(), max(8192, int64(time.Since(started).Seconds()*float64(*rate)))) }
		file, err := os.Open(fragmented)
		if err != nil {
			http.Error(w, "fixture unavailable", 500)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Cache-Control", "no-store")
		start, end := int64(0), int64(-1)
		raw := strings.TrimPrefix(r.Header.Get("Range"), "bytes=")
		if r.URL.Query().Get("ignore_range") == "1" {
			raw = ""
		}
		if parts := strings.SplitN(raw, "-", 2); len(parts) == 2 && parts[0] != "" {
			start, _ = strconv.ParseInt(parts[0], 10, 64)
			if parts[1] != "" {
				end, _ = strconv.ParseInt(parts[1], 10, 64)
			}
		}
		for start >= published() && published() < info.Size() {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
		if end >= 0 || start > 0 {
			size := published()
			if start >= size {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
				w.WriteHeader(416)
				return
			}
			if end < 0 || end >= size {
				end = size - 1
			}
			total := "*"
			if size == info.Size() {
				total = fmt.Sprint(size)
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%s", start, end, total))
			w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
			w.WriteHeader(206)
			if r.Method != "HEAD" {
				_, _ = io.CopyN(w, io.NewSectionReader(file, start, end-start+1), end-start+1)
			}
			log.Printf("range start=%d end=%d total=%s", start, end, total)
			return
		}
		w.WriteHeader(200)
		if r.Method == "HEAD" {
			return
		}
		flusher, _ := w.(http.Flusher)
		var offset int64
		buffer := make([]byte, 32768)
		for offset < info.Size() {
			available := published() - offset
			if available > 0 {
				n, err := file.ReadAt(buffer[:min(int64(len(buffer)), available)], offset)
				if n > 0 {
					if _, err = w.Write(buffer[:n]); err != nil {
						return
					}
					offset += int64(n)
					if flusher != nil {
						flusher.Flush()
					}
				}
				if err != nil && err != io.EOF {
					return
				}
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	})
	mux.Handle("/", http.FileServer(http.Dir(*directory)))
	log.Printf("transport fixtures at http://%s", *address)
	log.Fatal(http.ListenAndServe(*address, mux))
}
