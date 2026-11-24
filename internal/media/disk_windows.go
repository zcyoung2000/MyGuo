package media

import "golang.org/x/sys/windows"

func hasFreeDiskSpace(path string, minBytes uint64) bool {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(name, &available, nil, nil); err != nil {
		return true
	}
	return available >= minBytes
}
