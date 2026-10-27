//go:build ignore

// gen renders the app icon from icon/logo.jpg. Run it from the repository
// root after changing the logo:
//
//	go run icon/gen.go
//
// It writes resources/icon.png, a rounded square nearly edge to edge, which
// mygo build turns into the Windows and Linux icons (and the .icns
// fallback); icon/Assets.car, the macOS icon on Apple's grid (an 824px
// rounded square in 1024px, with its shadow), which needs Xcode's actool;
// and resources/icon-dev.png, the same grid for mygo dev, which shows only
// the .icns and does not ship the dev icon.
package main

import (
	"encoding/json"
	"fmt"
	"image"
	stddraw "image/draw"
	"image/jpeg"
	"image/png"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/image/draw"
)

const size = 1024

func main() {
	f, err := os.Open("icon/logo.jpg")
	if err != nil {
		log.Fatal(err)
	}
	logo, err := jpeg.Decode(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}

	if err := writePNG("resources/icon.png", rounded(logo, 40, false)); err != nil {
		log.Fatal(err)
	}
	mac := rounded(logo, 100, true)
	if err := writePNG("resources/icon-dev.png", mac); err != nil {
		log.Fatal(err)
	}
	if err := writeCar("icon/Assets.car", mac); err != nil {
		log.Fatal(err)
	}
}

// rounded draws logo into a superellipse (Apple's continuous corners) inset
// px from each edge of a size×size canvas, with the macOS drop shadow when
// shadow is set.
func rounded(logo image.Image, inset int, shadow bool) *image.RGBA {
	s := size - 2*inset
	art := image.NewNRGBA(image.Rect(0, 0, s, s))
	draw.CatmullRom.Scale(art, art.Bounds(), logo, logo.Bounds(), draw.Src, nil)

	// Coverage of the superellipse |x|^5 + |y|^5 <= 1, 4×4 samples a pixel.
	const n, ss = 5.0, 4
	pow := make([]float64, s*ss)
	for i := range pow {
		pow[i] = math.Pow(math.Abs((float64(i)+0.5)/float64(s*ss)*2-1), n)
	}
	mask := make([]float64, s*s)
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			in := 0
			for j := 0; j < ss; j++ {
				for i := 0; i < ss; i++ {
					if pow[x*ss+i]+pow[y*ss+j] <= 1 {
						in++
					}
				}
			}
			mask[y*s+x] = float64(in) / ss / ss
			art.Pix[(y*s+x)*4+3] = uint8(math.Round(mask[y*s+x] * 255))
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	if shadow {
		// Apple's template: black at 30%, 10px down, blurred 10px.
		a := make([]float64, size*size)
		for y := 0; y < s; y++ {
			copy(a[(y+inset+10)*size+inset:], mask[y*s:(y+1)*s])
		}
		for range 3 {
			blur(a, 5)
		}
		for i, v := range a {
			dst.Pix[i*4+3] = uint8(math.Round(v * 0.3 * 255))
		}
	}
	stddraw.Draw(dst, art.Bounds().Add(image.Pt(inset, inset)), art, image.Point{}, stddraw.Over)
	return dst
}

// blur is one box blur of radius r over the size×size plane a, across and
// then down.
func blur(a []float64, r int) {
	line := make([]float64, size)
	for pass := 0; pass < 2; pass++ {
		for k := 0; k < size; k++ {
			at := func(i int) *float64 {
				if pass == 0 {
					return &a[k*size+i]
				}
				return &a[i*size+k]
			}
			sum := 0.0
			for i := -r; i <= r; i++ {
				if i >= 0 && i < size {
					sum += *at(i)
				}
			}
			for i := 0; i < size; i++ {
				line[i] = sum / float64(2*r+1)
				if j := i - r; j >= 0 {
					sum -= *at(j)
				}
				if j := i + r + 1; j < size {
					sum += *at(j)
				}
			}
			for i := 0; i < size; i++ {
				*at(i) = line[i]
			}
		}
	}
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeCar compiles img, at every macOS icon size, into an asset catalog
// whose app icon is AppIcon, as macos.icon in mygo.json expects.
func writeCar(path string, img *image.RGBA) error {
	tmp, err := os.MkdirTemp("", "sshyard-icon")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	set := filepath.Join(tmp, "Assets.xcassets", "AppIcon.appiconset")
	if err := os.MkdirAll(set, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "Assets.xcassets", "Contents.json"), []byte(`{"info":{"author":"xcode","version":1}}`), 0o644); err != nil {
		return err
	}
	type entry struct {
		Idiom    string `json:"idiom"`
		Size     string `json:"size"`
		Scale    string `json:"scale"`
		Filename string `json:"filename"`
	}
	var images []entry
	for _, pt := range []int{16, 32, 128, 256, 512} {
		for _, scale := range []int{1, 2} {
			px := pt * scale
			name := fmt.Sprintf("icon_%d.png", px)
			if _, err := os.Stat(filepath.Join(set, name)); err != nil {
				small := image.NewRGBA(image.Rect(0, 0, px, px))
				draw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), draw.Src, nil)
				if err := writePNG(filepath.Join(set, name), small); err != nil {
					return err
				}
			}
			images = append(images, entry{"mac", fmt.Sprintf("%dx%d", pt, pt), fmt.Sprintf("%dx", scale), name})
		}
	}
	contents, _ := json.Marshal(map[string]any{"images": images, "info": map[string]any{"author": "xcode", "version": 1}})
	if err := os.WriteFile(filepath.Join(set, "Contents.json"), contents, 0o644); err != nil {
		return err
	}

	out := filepath.Join(tmp, "out")
	if err := os.Mkdir(out, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("xcrun", "actool", filepath.Join(tmp, "Assets.xcassets"),
		"--compile", out, "--platform", "macosx", "--target-device", "mac",
		"--minimum-deployment-target", "12.0", "--app-icon", "AppIcon",
		"--output-partial-info-plist", filepath.Join(tmp, "partial.plist"))
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("actool (needs Xcode on macOS): %v\n%s", err, b)
	}
	car, err := os.ReadFile(filepath.Join(out, "Assets.car"))
	if err != nil {
		return err
	}
	return os.WriteFile(path, car, 0o644)
}
