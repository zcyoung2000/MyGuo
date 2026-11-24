//go:build !darwin && !windows

package playerengine

import (
	"fmt"

	"myguo/internal/avkit"
)

func New() (avkit.Player, error) {
	return nil, fmt.Errorf("video playback is supported on macOS and Windows")
}
