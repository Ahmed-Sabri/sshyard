// Package gtkfix trims the room MyGo's window buttons take on Linux.
//
// With a hidden title bar MyGo shows the window buttons in a GtkHeaderBar
// laid over the page. GTK 3 gives a header bar an empty title label that is
// still MIN_TITLE_CHARS (5) characters wide, so the bar is about 80px wider
// than its buttons. MyGo reports the whole bar as the title bar's room, and
// the empty part takes the clicks over it, so the toolbar has to keep
// clear of it: a wide dead gap before the buttons. Shrinking the title's
// font to a pixel takes that width away. Drop this package once MyGo sizes
// the bar to its buttons.
package gtkfix

// CSS is the style that shrinks the empty title of the buttons' bar. The
// class is the one MyGo puts on that bar only.
const CSS = `headerbar.mygo-window-controls { padding-left: 0; }
headerbar.mygo-window-controls .title,
headerbar.mygo-window-controls .subtitle { font-size: 1px; min-width: 0; padding: 0; margin: 0; }`
