//go:build darwin || linux

package media

import "golang.org/x/sys/unix"

func hasFreeDiskSpace(path string, minBytes uint64) bool {
	var stats unix.Statfs_t
	if err := unix.Statfs(path, &stats); err != nil {
		return true
	}
	return stats.Bavail*uint64(stats.Bsize) >= minBytes
}
