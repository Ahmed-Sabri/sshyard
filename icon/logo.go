// Package icon provides the artwork used by the application icon.
package icon

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

//go:embed logo.jpg
var logo []byte

// Bitmap creates a bitmap for one application's UI thread.
func Bitmap() *ui.Bitmap {
	bmp, err := ui.DecodeBitmap(logo)
	if err != nil {
		panic("decode application logo: " + err.Error())
	}
	return bmp
}
