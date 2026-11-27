//go:build windows

package media

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	hevcDecoderOnce sync.Once
	hevcDecoderOK   bool
)

func hasHEVCDecoder() bool {
	hevcDecoderOnce.Do(func() {
		hevcDecoderOK = probeHEVCDecoder()
	})
	return hevcDecoderOK
}

type mfRegisterTypeInfo struct {
	majorType windows.GUID
	subtype   windows.GUID
}

func probeHEVCDecoder() bool {
	// COM apartment state is per-thread. MFTEnumEx requires COM, so keep this
	// short probe on one OS thread and balance initialization when we own it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	comInitialized := false
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err == nil || err == syscall.Errno(1) {
		comInitialized = true // S_OK or S_FALSE
	} else if err != syscall.Errno(windows.RPC_E_CHANGED_MODE) {
		return true // Unknown probe state: preserve the user's current behavior.
	}
	if comInitialized {
		defer windows.CoUninitialize()
	}

	mfplat := windows.NewLazySystemDLL("mfplat.dll")
	mfStartup := mfplat.NewProc("MFStartup")
	mfShutdown := mfplat.NewProc("MFShutdown")
	mftEnumEx := mfplat.NewProc("MFTEnumEx")
	if err := mfStartup.Find(); err != nil {
		return true
	}
	if err := mfShutdown.Find(); err != nil {
		return true
	}
	if err := mftEnumEx.Find(); err != nil {
		return true
	}
	if hr, _, _ := mfStartup.Call(0x00020070, 0); failedHRESULT(hr) {
		return true
	}
	defer mfShutdown.Call()

	category := windows.GUID{Data1: 0xd6c02d4b, Data2: 0x6833, Data3: 0x45b4, Data4: [8]byte{0x97, 0x1a, 0x05, 0xa4, 0xb0, 0x4b, 0xab, 0x91}}
	input := mfRegisterTypeInfo{
		majorType: windows.GUID{Data1: 0x73646976, Data2: 0, Data3: 0x0010, Data4: [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}},
		subtype:   windows.GUID{Data1: 0x43564548, Data2: 0, Data3: 0x0010, Data4: [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}},
	}
	const mftEnumFlagAll = 0x3f
	var activations *unsafe.Pointer
	var count uint32
	hr, _, _ := mftEnumEx.Call(
		uintptr(unsafe.Pointer(&category)),
		mftEnumFlagAll,
		uintptr(unsafe.Pointer(&input)),
		0,
		uintptr(unsafe.Pointer(&activations)),
		uintptr(unsafe.Pointer(&count)),
	)
	if failedHRESULT(hr) {
		return true
	}
	if count > 0 && activations == nil {
		return true // Inconsistent API result: do not switch away from HEVC.
	}
	if activations != nil {
		defer windows.CoTaskMemFree(unsafe.Pointer(activations))
	}
	for _, activation := range unsafe.Slice(activations, int(count)) {
		if activation == nil {
			continue
		}
		vtable := *(*[3]uintptr)(unsafe.Pointer(*(*uintptr)(activation)))
		syscall.SyscallN(vtable[2], uintptr(activation)) // IMFActivate::Release
	}
	return count > 0
}

func failedHRESULT(hr uintptr) bool { return int32(uint32(hr)) < 0 }
