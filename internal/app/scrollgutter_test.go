package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSheetScrollReachesIntoThePadding keeps a sheet's scrolling form as
// wide as its title, its scroll bar in the sheet's padding past the form.
func TestSheetScrollReachesIntoThePadding(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(480).Padding(24).Children(func() {
			ui.Row(c).FillWidth().Children(func() { ui.Text(c, "Title").Grow(1) })
			sheetScroll(ui.Scroll(c.Key("s")).MaxHeight(100)).Children(func() {
				ui.Row(c).FillWidth().Children(func() { ui.Text(c, "Field").Grow(1) })
				ui.Box(c).Height(400)
			})
		})
	}, 800, 600)
	tt.Frame()
	title, _ := tt.Find("Title")
	field, ok := tt.Find("Field")
	if !ok || field.X != title.X || field.W != title.W {
		t.Errorf("form %+v, title %+v: not as wide", field, title)
	}
}

// TestGutterScrollClearsTheBarWhileItOverflows narrows the content of a
// Scroll by the gutter only while it scrolls.
func TestGutterScrollClearsTheBarWhileItOverflows(t *testing.T) {
	tall := true
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(300).Children(func() {
			gutterScroll(c, ui.Scroll(c.Key("s")).MaxHeight(100).FillWidth()).Children(func() {
				ui.Row(c).FillWidth().Children(func() { ui.Text(c, "Row").Grow(1) })
				if tall {
					ui.Box(c).Height(400)
				}
			})
		})
	}, 800, 600)
	tt.Frame()
	if r, _ := tt.Find("Row"); r.W != 300-scrollGutter {
		t.Errorf("overflowing row %+v: want %v wide", r, 300-scrollGutter)
	}
	tall = false
	tt.Frame()
	tt.Frame()
	if r, _ := tt.Find("Row"); r.W != 300 {
		t.Errorf("row that fits %+v: want the full 300", r)
	}
}
