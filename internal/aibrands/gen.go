//go:build ignore

// gen scales the selected LobeHub artwork to the size the interface draws.
// Run it from the repository root with the package directory of the
// @lobehub/icons-static-png archive that NOTICE pins, extracted:
//
//	go run internal/aibrands/gen.go path/to/package
//
// It reads light/ and dark/ there for each PNG in png/light/, the selected
// brands, and writes png/light/ and png/dark/ at size px: the upstream
// 640px costs 1.6MB a bitmap decoded, for artwork that shows at 20pt at
// most.
package main

import (
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

// size is the side of each bitmap: 20pt, the largest the interface draws a
// brand, at 3×.
const size = 64

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: go run internal/aibrands/gen.go path/to/package")
	}
	out := filepath.Join("internal", "aibrands", "png")
	entries, err := os.ReadDir(filepath.Join(out, "light"))
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range entries {
		for _, theme := range []string{"light", "dark"} {
			if err := scale(filepath.Join(os.Args[1], theme, e.Name()), filepath.Join(out, theme, e.Name())); err != nil {
				log.Fatal(err)
			}
		}
	}
}

// scale writes the PNG at src to dst, size px square.
func scale(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	small := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), draw.Src, nil)
	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(w, small); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}
