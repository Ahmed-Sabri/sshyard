// Package icon provides the artwork used by the application icon.
package icon

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

// logo is logo.jpg at the side the UI draws it, rendered by gen.go: the
// 4096px source costs a decode of 80MB on every launch.
//
//go:embed logo-ui.png
var logo []byte

// uiSize is the side of logo, which the UI shows at 64pt at most.
const uiSize = 256

// Bitmap creates a bitmap for one application's UI thread.
func Bitmap() *ui.Bitmap {
	b, err := ui.DecodeBitmap(logo)
	if err != nil {
		panic("decode application logo: " + err.Error())
	}
	return b
}
