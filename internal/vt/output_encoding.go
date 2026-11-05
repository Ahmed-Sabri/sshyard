package vt

import (
	"fmt"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// OutputEncoding selects how printable program output is decoded.
type OutputEncoding string

const (
	EncodingUTF8 OutputEncoding = "UTF-8"
	EncodingGBK  OutputEncoding = "GBK"
)

func (t *Terminal) OutputEncoding() OutputEncoding {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.outputEncoding
}

// SetOutputEncoding affects future output only; an unfinished character is
// displayed as a replacement character without clearing existing text.
func (t *Terminal) SetOutputEncoding(encoding OutputEncoding) error {
	if encoding != EncodingUTF8 && encoding != EncodingGBK {
		return fmt.Errorf("vt: unsupported output encoding %q", encoding)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if encoding == t.outputEncoding {
		return nil
	}
	t.flushOutputCharacter()
	t.outputEncoding = encoding
	return nil
}

// ResetOutputStream starts a new program stream, retaining the screen and
// encoding selection. Unfinished characters become visible replacements;
// unfinished control sequences are discarded.
func (t *Terminal) ResetOutputStream() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushOutputCharacter()
	t.state = sGround
	t.osc = t.osc[:0]
	t.osc52, t.oscDrop = false, false
	t.onlyOSC = false
	t.resetDCS()
}

func (t *Terminal) flushOutputCharacter() {
	if len(t.utf8buf) != 0 || t.gbkLead != 0 {
		t.print(utf8.RuneError)
		t.gen++
	}
	t.utf8buf = t.utf8buf[:0]
	t.gbkLead = 0
}

// writeGBK runs under mu. Protocol bytes never pass through the text decoder.
func (t *Terminal) writeGBK(p []byte) (int, error) {
	decoder := simplifiedchinese.GBK.NewDecoder()
	for i, b := range p {
		if t.gbkLead != 0 {
			lead := t.gbkLead
			t.gbkLead = 0
			if b >= 0x40 && b <= 0xfe && b != 0x7f {
				var decoded [utf8.UTFMax]byte
				n, _, err := decoder.Transform(decoded[:], []byte{lead, b}, true)
				if err != nil {
					return i, fmt.Errorf("vt: decode GBK: %w", err)
				}
				r, _ := utf8.DecodeRune(decoded[:n])
				t.print(r)
				continue
			}
			// Invalid trails are processed again so C0 and ESC still act.
			t.print(utf8.RuneError)
		}
		if t.state != sGround || b < 0x80 {
			t.byte(b)
			continue
		}
		switch {
		case b == 0x80:
			t.print('€') // GBK's CP936 extension, matching x/text.
		case b < 0xff:
			t.gbkLead = b
		default:
			t.print(utf8.RuneError)
		}
	}
	return len(p), nil
}
