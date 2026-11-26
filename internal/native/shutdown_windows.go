package native

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shutdownUser32    = windows.NewLazySystemDLL("user32.dll")
	shutdownPeek      = shutdownUser32.NewProc("PeekMessageW")
	shutdownTranslate = shutdownUser32.NewProc("TranslateMessage")
	shutdownDispatch  = shutdownUser32.NewProc("DispatchMessageW")
	shutdownWait      = shutdownUser32.NewProc("MsgWaitForMultipleObjectsEx")
	shutdownPostQuit  = shutdownUser32.NewProc("PostQuitMessage")
)

func shutdownStep() {
	// MSG, including the private trailing field, has the Win32 ABI layout.
	var message struct {
		Window  uintptr
		ID      uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		X, Y    int32
		Private uint32
	}
	shutdownWait.Call(0, 0, 10, 0x04ff, 0x0004) // QS_ALLINPUT, MWMO_INPUTAVAILABLE
	ptr := uintptr(unsafe.Pointer(&message))
	if available, _, _ := shutdownPeek.Call(ptr, 0, 0, 0, 1); available == 0 {
		return
	}
	if message.ID == 0x0012 { // WM_QUIT belongs to the outer application loop.
		shutdownPostQuit.Call(message.WParam)
		return
	}
	shutdownTranslate.Call(ptr)
	shutdownDispatch.Call(ptr)
}
