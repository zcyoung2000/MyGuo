package native

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var shutdownRunLoop = sync.OnceValue(func() func() {
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}
	mode, err := purego.Dlsym(lib, "kCFRunLoopDefaultMode")
	if err != nil {
		panic(err)
	}
	var run func(uintptr, float64, bool) int32
	purego.RegisterLibFunc(&run, lib, "CFRunLoopRunInMode")
	defaultMode := *(*uintptr)(unsafe.Pointer(mode))
	return func() { run(defaultMode, .01, true) }
})

func shutdownStep() { shutdownRunLoop()() }
