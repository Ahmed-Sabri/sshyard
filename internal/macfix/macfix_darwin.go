//go:build darwin

package macfix

import "github.com/ebitengine/purego/objc"

type nsPoint struct{ X, Y float64 }

// mouseEvent is how the monitor handles a mouse event: the selector it
// goes to the page's view with, whether it is a press, and the type of the
// release that ends its press.
type mouseEvent struct {
	sel     objc.SEL
	down    bool
	release uint
}

// mouseEvents are the events the monitor sees, by their NSEventType.
var mouseEvents = map[uint]mouseEvent{
	3:  {objc.RegisterName("rightMouseDown:"), true, 4},
	4:  {objc.RegisterName("rightMouseUp:"), false, 4},
	7:  {objc.RegisterName("rightMouseDragged:"), false, 4},
	25: {objc.RegisterName("otherMouseDown:"), true, 26},
	26: {objc.RegisterName("otherMouseUp:"), false, 26},
	27: {objc.RegisterName("otherMouseDragged:"), false, 26},
}

// monitor is the event monitor, made once.
var monitor objc.ID

// routed is the page's view that takes the press the monitor rerouted,
// with its drags and release, by the type of the press's release.
// Main thread only.
var routed = map[uint]objc.ID{}

var (
	selType        = objc.RegisterName("type")
	selWindow      = objc.RegisterName("window")
	selLocation    = objc.RegisterName("locationInWindow")
	selContentView = objc.RegisterName("contentView")
	selSuperview   = objc.RegisterName("superview")
	selSubviews    = objc.RegisterName("subviews")
	selCount       = objc.RegisterName("count")
	selObjectAt    = objc.RegisterName("objectAtIndex:")
	selHitTest     = objc.RegisterName("hitTest:")
	selConvert     = objc.RegisterName("convertPoint:fromView:")
	selIsKindOf    = objc.RegisterName("isKindOfClass:")
)

// TitleBarClicks has the right and middle presses that AppKit's title bar
// keeps go to the page under them (see the package's doc), and reports
// whether it could. Call it on the main thread once the app is ready.
func TitleBarClicks() bool {
	if monitor != 0 {
		return true
	}
	surface, titleBar := objc.GetClass("MyGoSurfaceView"), objc.GetClass("NSTitlebarContainerView")
	if surface == 0 || titleBar == 0 {
		return false
	}
	control := objc.GetClass("NSControl")
	var mask uint64
	for typ := range mouseEvents {
		mask |= 1 << typ
	}
	blk := objc.NewBlock(func(_ objc.Block, ev objc.ID) objc.ID {
		typ := objc.Send[uint](ev, selType)
		e, ok := mouseEvents[typ]
		if !ok {
			return ev
		}
		if e.down {
			routed[e.release] = pageUnder(ev, surface, titleBar, control)
		}
		view := routed[e.release]
		if view == 0 {
			return ev
		}
		if typ == e.release {
			delete(routed, e.release)
		}
		view.Send(e.sel, ev)
		return 0
	})
	monitor = objc.ID(objc.GetClass("NSEvent")).Send(objc.RegisterName("addLocalMonitorForEventsMatchingMask:handler:"), mask, blk)
	if monitor == 0 {
		return false
	}
	monitor.Send(objc.RegisterName("retain"))
	return true
}

// pageUnder returns the page's view (of class surface) under the press ev
// when the window would give the press to its title bar (inside a view of
// class titleBar) other than to one of its buttons (control), or 0.
func pageUnder(ev objc.ID, surface, titleBar, control objc.Class) objc.ID {
	win := ev.Send(selWindow)
	if win == 0 {
		return 0
	}
	content := win.Send(selContentView)
	frame := content.Send(selSuperview)
	if frame == 0 {
		return 0
	}
	at := objc.Send[nsPoint](ev, selLocation)
	hit := objc.Send[objc.ID](frame, selHitTest, at)
	inTitleBar := false
	for v := hit; v != 0; v = v.Send(selSuperview) {
		if kindOf(v, control) {
			return 0
		}
		if kindOf(v, titleBar) {
			inTitleBar = true
			break
		}
	}
	if !inTitleBar {
		return 0
	}
	page := find(content, surface)
	if page == 0 {
		return 0
	}
	// Not where the page's view lets the pointer through to a web view.
	up := page.Send(selSuperview)
	if objc.Send[objc.ID](page, selHitTest, objc.Send[nsPoint](up, selConvert, at, objc.ID(0))) == 0 {
		return 0
	}
	return page
}

// find returns the first view of class cls in v and its subviews, or 0.
func find(v objc.ID, cls objc.Class) objc.ID {
	if kindOf(v, cls) {
		return v
	}
	subs := v.Send(selSubviews)
	for i := range objc.Send[int](subs, selCount) {
		if f := find(objc.Send[objc.ID](subs, selObjectAt, i), cls); f != 0 {
			return f
		}
	}
	return 0
}

func kindOf(v objc.ID, cls objc.Class) bool {
	return cls != 0 && objc.Send[bool](v, selIsKindOf, cls)
}
