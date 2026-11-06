// Package macfix works around a thing AppKit does with MyGo's windows on
// macOS.
//
// TitleBarClicks gives the page the right and middle clicks over the title
// bar. With a hidden title bar (TitleBarHiddenInset) the page draws the
// title area, the tabs among it, but AppKit keeps its title bar's views
// over the page, transparent. On some macOS releases the title bar's view
// takes the presses over it: a left press goes on to the page under it,
// whose view does not move the window, but a right or middle one stays
// with the title bar, so the tabs' context menus never open. A local event
// monitor sends such presses, with their drags and releases, to the page's
// view instead, where it is under the pointer; the window's buttons keep
// theirs. Where the title bar lets presses through, as on macOS 27, the
// monitor has nothing to do. Drop it once MyGo routes them itself.
package macfix
