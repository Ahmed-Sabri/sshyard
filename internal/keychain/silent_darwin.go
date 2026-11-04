package keychain

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The login keychain asks before an app outside an item's access list reads
// it; with user interaction disabled the read fails with
// errSecInteractionNotAllowed (-25308) instead. The setting is process-wide,
// so reads that may prompt are serialized here.
var interaction = sync.OnceValues(func() (struct{ get, set uintptr }, error) {
	var f struct{ get, set uintptr }
	lib, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return f, err
	}
	if f.get, err = purego.Dlsym(lib, "SecKeychainGetUserInteractionAllowed"); err != nil {
		return f, err
	}
	f.set, err = purego.Dlsym(lib, "SecKeychainSetUserInteractionAllowed")
	return f, err
})

var interactionMu sync.Mutex

func silently(get func() ([]byte, error)) ([]byte, error) {
	f, err := interaction()
	if err != nil {
		return nil, ErrInteractionRequired
	}
	interactionMu.Lock()
	defer interactionMu.Unlock()
	var allowed uint8
	if r, _, _ := purego.SyscallN(f.get, uintptr(unsafe.Pointer(&allowed))); int32(r) != 0 {
		return nil, ErrInteractionRequired
	}
	if r, _, _ := purego.SyscallN(f.set, 0); int32(r) != 0 {
		return nil, ErrInteractionRequired
	}
	defer purego.SyscallN(f.set, uintptr(allowed))
	return get()
}
