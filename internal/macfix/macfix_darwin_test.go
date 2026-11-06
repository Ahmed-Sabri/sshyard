//go:build darwin

package macfix

import "testing"

// Before MyGo has made a window its view class does not exist: there is no
// page to send presses to, so no monitor is installed.
func TestTitleBarClicksNeedsMyGoWindow(t *testing.T) {
	if TitleBarClicks() {
		t.Fatal("TitleBarClicks installed a monitor without MyGo's view class")
	}
	if monitor != 0 {
		t.Fatal("monitor set without MyGo's view class")
	}
}

// A press and the drags after it end with the same release, which closes
// the route the press opened.
func TestMouseEventsEndWithTheirRelease(t *testing.T) {
	for typ, e := range mouseEvents {
		r, ok := mouseEvents[e.release]
		if !ok || r.down || r.release != e.release {
			t.Errorf("event %d ends with %d, not a release of its own", typ, e.release)
		}
		if e.down && typ == e.release {
			t.Errorf("press %d is its own release", typ)
		}
	}
}
