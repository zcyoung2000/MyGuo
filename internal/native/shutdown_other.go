//go:build !darwin && !windows

package native

import "time"

// MyGuo ships native players only on macOS and Windows.
func shutdownStep() { time.Sleep(time.Millisecond) }
