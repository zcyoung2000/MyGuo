//go:build !darwin && !linux && !windows

package media

func hasFreeDiskSpace(path string, minBytes uint64) bool { return true }
