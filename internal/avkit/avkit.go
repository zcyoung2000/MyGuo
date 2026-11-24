// Package avkit hosts system video without a web document. MyGo v0.9.0
// ui/video lacks PiP, buffered ranges and preroll in its public API; this
// AVKit view supplies them while ui.NativeView owns placement and overlays.
package avkit

import (
	"context"
	"github.com/egoist/mygo/native"
	"io"
)

type Status struct {
	Position         float64
	Duration         float64
	Buffered         float64
	Seekable         float64
	Ready            bool
	Displayed        bool
	Playing          bool
	Buffering        bool
	Ended            bool
	PictureInPicture bool
	// RangesUnsupported is set by players whose public API cannot publish
	// buffered/seekable ranges (Windows Media Foundation via MyGo ui/video).
	// Callers must not infer playback runway from Buffered or Seekable then.
	RangesUnsupported bool
	Width             int
	Height            int
	Error             string
	AmbientLeft       []int
	AmbientTop        []int
}

// Player owns one AVPlayer. Calls and destruction execute on AppKit's main
// thread, including calls made by Go worker goroutines.
//
//mygo:native
type Player interface {
	native.View
	io.Closer
	Load(url string) error
	Play()
	Pause()
	Seek(seconds float64)
	Configure(rate, volume float64, muted bool, fit string)
	Preroll()
	Snapshot() Status
	// CaptureFrame returns one bounded JPEG for a feed transition. Continuous
	// playback stays in AVKit and never copies video frames through Go.
	CaptureFrame() []byte
	TogglePictureInPicture(ctx context.Context) error
}
