package icons

import (
	"strings"
	"testing"
)

func TestEveryFileIsUsedAndParses(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("no icons embedded")
	}
	for _, n := range names {
		ic, ok := Get(n)
		if !ok {
			t.Errorf("svg/%s.svg is embedded but has no variable", n)
			continue
		}
		if w, h := ic.SVG.Size(); w != 24 || h != 24 {
			t.Errorf("%s is %vx%v, want Remix Icon's 24x24", n, w, h)
		}
	}
	if len(all) != len(names) {
		t.Errorf("%d variables, %d files", len(all), len(names))
	}
}

func TestStyles(t *testing.T) {
	for n := range all {
		if !strings.HasSuffix(n, "-line") && !strings.HasSuffix(n, "-fill") {
			t.Errorf("%s is neither a -line nor a -fill icon", n)
		}
		if base, ok := strings.CutSuffix(n, "-fill"); ok {
			if _, has := all[base+"-line"]; !has {
				t.Errorf("%s has no -line counterpart; fill is only for active states", n)
			}
		}
	}
}
