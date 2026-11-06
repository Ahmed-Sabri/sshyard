package gtkfix

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestCSSTargetsOnlyMyGoControls(t *testing.T) {
	for _, block := range strings.Split(CSS, "}") {
		rule, _, ok := strings.Cut(block, "{")
		if !ok {
			continue
		}
		for _, sel := range strings.Split(rule, ",") {
			if sel = strings.TrimSpace(sel); sel != "headerbar.mygo-window-controls" && !strings.HasPrefix(sel, "headerbar.mygo-window-controls ") {
				t.Errorf("selector %q reaches beyond the window buttons' bar", sel)
			}
		}
	}
}

// The fontconfig rules set the subpixel order to none, for patterns and
// fonts, and nothing else.
func TestFontconfigOnlyClearsSubpixelOrder(t *testing.T) {
	var conf struct {
		Match []struct {
			Target string     `xml:"target,attr"`
			Test   []struct{} `xml:"test"`
			Edit   []struct {
				Name  string `xml:"name,attr"`
				Mode  string `xml:"mode,attr"`
				Const string `xml:"const"`
			} `xml:"edit"`
		} `xml:"match"`
	}
	if err := xml.Unmarshal([]byte(Fontconfig), &conf); err != nil {
		t.Fatal(err)
	}
	targets := map[string]bool{}
	for _, m := range conf.Match {
		targets[m.Target] = true
		if len(m.Test) != 0 || len(m.Edit) != 1 {
			t.Errorf("match %q has %d tests and %d edits, want one edit for every font", m.Target, len(m.Test), len(m.Edit))
			continue
		}
		if e := m.Edit[0]; e.Name != "rgba" || e.Mode != "assign" || e.Const != "none" {
			t.Errorf("match %q edits %s (%s) to %q, want rgba assigned none", m.Target, e.Name, e.Mode, e.Const)
		}
	}
	if len(conf.Match) != 2 || !targets["pattern"] || !targets["font"] {
		t.Errorf("targets %v, want pattern and font", targets)
	}
}
