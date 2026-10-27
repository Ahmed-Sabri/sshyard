package vt

import (
	"strings"
	"testing"
)

func TestCSIBoundedAcrossChunks(t *testing.T) {
	for _, attack := range []string{strings.Repeat(";", 1<<20), strings.Repeat(":", 1<<20), strings.Repeat(" ", 1<<20), strings.Repeat("9", 1<<20)} {
		term := New(20, 3)
		bells := 0
		term.Bell = func() { bells++ }
		term.WriteString("SAFE\x1b[")
		for start := 0; start < len(attack); start += 127 {
			term.WriteString(attack[start:min(start+127, len(attack))])
		}
		if term.state != sCSIDrop || len(term.params) > maxCSIParams || len(term.sub) > maxCSIParams || len(term.inter) > maxCSIIntermediates {
			t.Fatalf("unbounded or not discarded: %d %d %d state %d", len(term.params), len(term.sub), len(term.inter), term.state)
		}
		// Controls, Unicode, and ESC cannot restart an oversized sequence.
		term.WriteString("\a\r\n\x1b\x1bé")
		if bells != 0 || term.ScreenText() != "SAFE" {
			t.Fatalf("discarded suffix acted: bells %d screen %q", bells, term.ScreenText())
		}
		term.WriteString("mOK\x1b[31mR")
		if !strings.HasPrefix(term.ScreenText(), "SAFEOKR") || term.state != sGround {
			t.Fatalf("did not recover: %q", term.ScreenText())
		}
	}
}

func TestCSILargeNumberSaturates(t *testing.T) {
	term := New(20, 3)
	term.WriteString("\x1b[" + strings.Repeat("9", 1000))
	if term.paramCur != 65535 || term.state != sCSI {
		t.Fatalf("numeric saturation: %d state %d", term.paramCur, term.state)
	}
	term.WriteString("CX")
	if cell(term, 19, 0).Rune != 'X' {
		t.Fatalf("saturated move did not clamp: %q", term.ScreenText())
	}
}
