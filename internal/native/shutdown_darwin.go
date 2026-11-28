package native

import (
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var shutdownRunLoop = sync.OnceValue(func() func() {
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}
	var run func(uintptr, float64, bool) int32
	purego.RegisterLibFunc(&run, lib, "CFRunLoopRunInMode")
	defaultMode := objc.ID(objc.GetClass("NSString")).Send(
		objc.RegisterName("stringWithUTF8String:"), "kCFRunLoopDefaultMode\x00",
	).Send(objc.RegisterName("retain"))
	return func() { run(uintptr(defaultMode), .01, true) }
})

func shutdownStep() { shutdownRunLoop()() }
