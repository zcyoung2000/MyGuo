package ipc

import (
	"testing"

	"myguo/internal/media"
)

func TestFullPrefetchWindowKeepsDownloadsOnTransientEmptyLookahead(t *testing.T) {
	s := &HongguoService{media: &media.MediaServer{}}
	neighbor := []media.FeedPreloadItem{{EpisodeID: "ep-next", SeriesID: "series", Number: 2, Quality: "720p"}}

	// Establish a real window first. Any spurious ReplacePrefetchWindow would
	// then be observable as a change of the published signature.
	s.updateFullPrefetchWindow(1, "ep-current", "720p", neighbor)
	initial := s.fullPrefetchSignature
	if initial == "" || initial == "||" {
		t.Fatalf("expected a populated window signature, got %q", initial)
	}

	// Auto-advance promotes ep-next while its own neighbor is unresolved and
	// its status is not Ready. The renderer submits an empty lookahead; the
	// full-file download of ep-next must survive so promotion can be instant.
	s.updateFullPrefetchWindow(2, "ep-next", "720p", nil)
	if got := s.fullPrefetchSignature; got != initial {
		t.Fatalf("transient empty lookahead replaced the prefetch window: signature %q -> %q", initial, got)
	}

	// Only invalid/duplicate neighbor IDs must not count as a real lookahead.
	s.updateFullPrefetchWindow(3, "ep-next", "720p", []media.FeedPreloadItem{{EpisodeID: ""}, {EpisodeID: "ep-next"}})
	if got := s.fullPrefetchSignature; got != initial {
		t.Fatalf("invalid lookahead replaced the prefetch window: signature %q -> %q", initial, got)
	}
}

func TestFullPrefetchWindowClearsOnExplicitReset(t *testing.T) {
	s := &HongguoService{media: &media.MediaServer{}}
	s.updateFullPrefetchWindow(1, "ep-current", "720p", []media.FeedPreloadItem{
		{EpisodeID: "ep-next", SeriesID: "series", Number: 2, Quality: "720p"},
	})
	if s.fullPrefetchSignature == "" {
		t.Fatal("expected a populated window signature")
	}

	// reset() and dispose() send an empty current episode plus nil items.
	// That is an intentional teardown and must still clear the window.
	s.updateFullPrefetchWindow(2, "", "", nil)
	if got := s.fullPrefetchSignature; got != "|" {
		t.Fatalf("reset should clear the prefetch window, got signature %q", got)
	}
}

func TestFullPrefetchWindowRejectsOutOfOrderUpdates(t *testing.T) {
	for _, scenario := range []string{"stale-window", "stale-reset", "duplicate-version", "after-transient-empty", "after-reset"} {
		t.Run(scenario, func(t *testing.T) {
			s := &HongguoService{media: &media.MediaServer{}}
			s.updateFullPrefetchWindow(2, "current", "720p", []media.FeedPreloadItem{{EpisodeID: "next", Quality: "720p"}})
			staleVersion := int64(1)
			switch scenario {
			case "duplicate-version":
				staleVersion = 2
			case "after-transient-empty":
				s.updateFullPrefetchWindow(3, "next", "720p", nil)
				staleVersion = 2
			case "after-reset":
				s.updateFullPrefetchWindow(3, "", "", nil)
				staleVersion = 2
			}
			want := s.fullPrefetchSignature
			if scenario == "stale-reset" {
				s.updateFullPrefetchWindow(staleVersion, "", "", nil)
			} else {
				s.updateFullPrefetchWindow(staleVersion, "old", "1080p", []media.FeedPreloadItem{{EpisodeID: "old-next", Quality: "1080p"}})
			}
			if got := s.fullPrefetchSignature; got != want {
				t.Fatalf("out-of-order update rewound the active full-file window: %q -> %q", want, got)
			}
		})
	}
}
