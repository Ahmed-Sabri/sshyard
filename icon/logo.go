// Package icon provides the artwork used by the application icon.
package icon

import (
	"bytes"
	_ "embed"
	"image"
	_ "image/jpeg"
	"sync"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
)

//go:embed logo.jpg
var logo []byte

// uiSize is the side of the logo the UI draws, which shows it at 64pt at
// most: the 4096px source costs a decode of 64MB on every app.
const uiSize = 256

// small is logo decoded once and scaled to uiSize.
var small = sync.OnceValue(func() *image.RGBA {
	src, _, err := image.Decode(bytes.NewReader(logo))
	if err != nil {
		panic("decode application logo: " + err.Error())
	}
	dst := image.NewRGBA(image.Rect(0, 0, uiSize, uiSize))
	draw.BiLinear.Scale(dst, dst.Rect, src, src.Bounds(), draw.Src, nil)
	return dst
})

// Bitmap creates a bitmap for one application's UI thread.
func Bitmap() *ui.Bitmap {
	s := small()
	img := image.NewRGBA(s.Rect)
	copy(img.Pix, s.Pix)
	return ui.NewBitmap(img)
}
