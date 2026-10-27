//go:build linux

package gtkfix

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// Apply adds CSS for the default screen, through the GTK that MyGo has
// already loaded. Without GTK it does nothing. Call it on the main thread
// once the app is ready.
func Apply() bool {
	sym := func(name string) uintptr {
		p, err := purego.Dlsym(purego.RTLD_DEFAULT, name)
		if err != nil {
			return 0
		}
		return p
	}
	newProvider, load := sym("gtk_css_provider_new"), sym("gtk_css_provider_load_from_data")
	screen, add := sym("gdk_screen_get_default"), sym("gtk_style_context_add_provider_for_screen")
	unref := sym("g_object_unref")
	if newProvider == 0 || load == 0 || screen == 0 || add == 0 {
		return false
	}
	s, _, _ := purego.SyscallN(screen)
	if s == 0 {
		return false
	}
	p, _, _ := purego.SyscallN(newProvider)
	if p == 0 {
		return false
	}
	css := append([]byte(CSS), 0)
	purego.SyscallN(load, p, uintptr(unsafe.Pointer(&css[0])), uintptr(len(CSS)), 0)
	// Above MyGo's own style (600, GTK_STYLE_PROVIDER_PRIORITY_APPLICATION).
	purego.SyscallN(add, s, p, 700)
	if unref != 0 {
		purego.SyscallN(unref, p)
	}
	return true
}
