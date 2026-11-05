//go:build ignore

// traygen generates tray-only artwork without changing the application icons
// or their sources. Run from the repository root: go run icon/traygen.go
//
// tray-white.svg is the authoritative monochrome vector, traced from the
// existing SSHYard logo silhouette with its white background removed.
// MyGo's tray accepts PNG, so its native SVG renderer is used here at
// generation time, never during app startup.
package main

import (
	"image/png"
	"log"
	"os"

	"github.com/egoist/mygo/ui"
)

const traySize = 36

func main() {
	source, err := os.ReadFile("icon/tray-white.svg")
	check(err)
	svg, err := ui.ParseSVG(source)
	check(err)
	logo, err := os.ReadFile("icon/logo-ui.png")
	check(err)
	bitmap, err := ui.DecodeBitmap(logo)
	check(err)

	for _, style := range []string{"white", "black", "color"} {
		t := ui.NewTester(func(c *ui.Context) {
			theme := *ui.LightTheme()
			theme.Background = ui.Transparent
			c.SetTheme(&theme)
			if style == "color" {
				ui.Image(c, bitmap).Size(traySize, traySize).Radius(8)
				return
			}
			ink := ui.RGB(255, 255, 255)
			if style == "black" {
				ink = ui.RGB(0, 0, 0)
			}
			ui.Icon(c, svg).Size(traySize, traySize).TextColor(ink)
		}, traySize, traySize)
		f, err := os.Create("icon/tray-" + style + ".png")
		check(err)
		check(png.Encode(f, t.Image()))
		check(f.Close())
	}
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
