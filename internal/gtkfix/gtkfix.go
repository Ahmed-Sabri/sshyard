// Package gtkfix works around two things MyGo does on Linux.
//
// Apply trims the room MyGo's window buttons take. With a hidden title bar
// MyGo shows the window buttons in a GtkHeaderBar laid over the page. GTK 3
// gives a header bar an empty title label that is still MIN_TITLE_CHARS (5)
// characters wide, so the bar is about 80px wider than its buttons. MyGo
// reports the whole bar as the title bar's room, and the empty part takes
// the clicks over it, so the toolbar has to keep clear of it: a wide dead
// gap before the buttons. Shrinking the title's font to a pixel takes that
// width away. Drop it once MyGo sizes the bar to its buttons.
//
// TextColor keeps text in its own color. MyGo rasterizes glyphs with cairo
// in grayscale and takes any glyph whose channels differ for a color glyph
// (an emoji), drawn in its own color. cairo before 1.17.8 turns grayscale
// into subpixel antialiasing wherever the font fontconfig matched has a
// subpixel order (rgba), from the desktop's subpixel setting or from
// fontconfig's own rules, so every glyph comes out white with a green
// alpha, whatever the text's color: pale green text, unreadable on light
// fills and tooltips (Debian 12, Ubuntu 22.04, RHEL 8/9, KylinSec and the
// like). Matching fonts without a subpixel order keeps grayscale gray;
// MyGo's own subpixel glyphs still ask for their order. Drop it once MyGo
// forces grayscale itself or no longer supports such a cairo.
package gtkfix

// CSS is the style that shrinks the empty title of the buttons' bar. The
// class is the one MyGo puts on that bar only.
const CSS = `headerbar.mygo-window-controls { padding-left: 0; }
headerbar.mygo-window-controls .title,
headerbar.mygo-window-controls .subtitle { font-size: 1px; min-width: 0; padding: 0; margin: 0; }`

// Fontconfig is the configuration TextColor adds after the system's: fonts
// match without a subpixel order, as patterns and as fonts.
const Fontconfig = `<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
<fontconfig>
  <match target="pattern"><edit name="rgba" mode="assign"><const>none</const></edit></match>
  <match target="font"><edit name="rgba" mode="assign"><const>none</const></edit></match>
</fontconfig>
`
