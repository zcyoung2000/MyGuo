package playerengine

import "myguo/internal/avkit"

// New keeps AVKit's native preroll, frame capture and PiP on macOS.
func New() (avkit.Player, error) { return avkit.NewPlayer() }
