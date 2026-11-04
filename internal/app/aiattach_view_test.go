package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func attachmentTester(t *testing.T, width int, dark bool, files *[]*aiAttach, removable bool) (*App, *ui.Tester) {
	t.Helper()
	a := &App{}
	tt := ui.NewTester(func(c *ui.Context) {
		tok := theme.For(theme.Auto, c.Theme().Dark)
		c.SetTheme(tok.Apply(*c.Theme()))
		c.Root().Background(tok.Canvas)
		ui.Column(c).Fill().Padding(theme.Space(2)).Children(func() {
			var remove func(int)
			if removable {
				remove = func(i int) { *files = append((*files)[:i], (*files)[i+1:]...) }
			}
			a.attachStrip(c, tok, locales.In(c.Locale()), *files, remove)
		})
		a.aiAttachmentPreviewView(c, locales.In(c.Locale()), tok)
	}, width, 480)
	tt.SetLocale("en")
	tt.SetDark(dark)
	tt.Frame()
	return a, tt
}

func TestAIAttachmentCards(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{640, 180} {
			t.Run(fmt.Sprintf("dark=%v/width=%d", dark, width), func(t *testing.T) {
				img, _ := attachData("a-very-long-image-file-name.png", pngBytes(t))
				pdf, _ := attachData("document.pdf", []byte("%PDF-1.7\n..."))
				txt, _ := attachData("notes.txt", []byte("notes"))
				files := []*aiAttach{img, pdf, txt}
				_, tt := attachmentTester(t, width, dark, &files, true)
				var previous ui.Rect
				for i, f := range files {
					card := mustFind(t, tt, f.name)
					rm := mustFind(t, tt, "Remove "+f.name)
					if card.X < 0 || card.X+card.W > float32(width) || rm.W == 0 || rm.H == 0 || rm.X < card.X || rm.X+rm.W > card.X+card.W || rm.Y < card.Y || rm.Y+rm.H > card.Y+card.H {
						t.Fatalf("card %v or remove %v exceeds bounds", card, rm)
					}
					if width == 180 && i > 0 && card.Y < previous.Y+previous.H {
						t.Fatalf("cards did not wrap: %v and %v", previous, card)
					}
					if !tt.HasText(f.format() + " · " + humanSize(int64(len(f.data)))) {
						t.Fatalf("missing format and size: %q", tt.Texts())
					}
					previous = card
				}
				for len(files) > 0 {
					rm := mustFind(t, tt, "Remove "+files[0].name)
					before := len(files)
					tt.ClickAt(rm.X+rm.W/2, rm.Y+rm.H/2)
					tt.Frame()
					if len(files) != before-1 {
						t.Fatal("mouse did not remove attachment")
					}
				}
			})
		}
	}
}

func TestAIAttachmentPreview(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, removable := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/composer=%v", dark, removable), func(t *testing.T) {
				img, _ := attachData("shot.png", pngBytes(t))
				files := []*aiAttach{img}
				a, tt := attachmentTester(t, 320, dark, &files, removable)
				if !removable {
					for _, text := range tt.Texts() {
						if strings.HasPrefix(text, "Remove ") {
							t.Fatal("sent attachment has remove action")
						}
					}
				}
				for _, close := range []string{"button", "escape", "outside"} {
					mustClick(t, tt, "Preview shot.png")
					tt.Frame()
					if a.attachmentPreview == nil || !tt.HasText("Close Preview") {
						t.Fatal("image preview did not open")
					}
					p := mustFind(t, tt, "Close Preview")
					if p.X+p.W > 320 || p.W == 0 {
						t.Fatalf("preview close out of bounds: %v", p)
					}
					switch close {
					case "button":
						mustClick(t, tt, "Close Preview")
					case "escape":
						tt.Key(0, ui.KeyEscape)
					case "outside":
						tt.ClickAt(1, 1)
					}
					tt.Frame()
					if a.attachmentPreview != nil || tt.HasText("Close Preview") {
						t.Fatalf("preview did not close via %s", close)
					}
					tt.Frame()
					if a.attachmentPreview != nil {
						t.Fatal("preview reopened on next frame")
					}
				}
			})
		}
	}
}

func TestAIAttachmentDuplicateNames(t *testing.T) {
	first, _ := attachData("same.png", pngBytes(t))
	second, _ := attachData("same.png", pngBytes(t))
	files := []*aiAttach{first, second}
	a, tt := attachmentTester(t, 640, false, &files, true)
	mustClick(t, tt, "Remove same.png")
	tt.Frame()
	if len(files) != 1 || files[0] != second {
		t.Fatal("removal did not preserve the other file with the same name")
	}
	mustClick(t, tt, "Preview same.png")
	tt.Frame()
	if a.attachmentPreview == nil || a.attachmentPreview.file != second {
		t.Fatal("remaining duplicate previewed the wrong image")
	}
	mustClick(t, tt, "Close Preview")
	tt.Frame()
	mustClick(t, tt, "Remove same.png")
	tt.Frame()
	if len(files) != 0 {
		t.Fatal("remaining duplicate did not remove")
	}
}
