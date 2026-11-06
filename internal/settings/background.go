package settings

import "strings"

// Background belongs to this device; Image names a managed background file.
type Background struct {
	Image          string `json:"image,omitempty"`
	Fit            string `json:"fit"`
	ChromeOpacity  int    `json:"chromeOpacity"`
	ContentOpacity int    `json:"contentOpacity"`
}

func DefaultBackground() Background {
	return Background{Fit: "cover", ChromeOpacity: 70, ContentOpacity: 80}
}

// BackgroundImageOK accepts managed basenames only, on every platform.
func BackgroundImageOK(name string) bool {
	if !strings.HasPrefix(name, "background-") || len(name) > 100 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return false
		}
	}
	return strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpeg") || strings.HasSuffix(name, ".webp")
}

func (b *Background) normalize() {
	d := DefaultBackground()
	if b.Image != "" && !BackgroundImageOK(b.Image) {
		b.Image = ""
	}
	if b.Fit != "cover" && b.Fit != "contain" {
		b.Fit = d.Fit
	}
	if b.ChromeOpacity < 0 || b.ChromeOpacity > 100 {
		b.ChromeOpacity = d.ChromeOpacity
	}
	if b.ContentOpacity < 0 || b.ContentOpacity > 100 {
		b.ContentOpacity = d.ContentOpacity
	}
}
