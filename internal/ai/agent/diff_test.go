package agent

import (
	"strings"
	"testing"
)

func TestUnifiedNewFileIsFullText(t *testing.T) {
	got := Unified("nginx.conf", "", "server {\n}\n")
	if got != "server {\n}\n" {
		t.Fatalf("%q", got)
	}
	if Unified("a", "same\n", "same\n") != "" {
		t.Fatal("identical files produced a diff")
	}
}

func TestUnifiedChange(t *testing.T) {
	old := "a\nb\nc\n"
	neu := "a\nx\nc\n"
	got := Unified("f.txt", old, neu)
	if !strings.Contains(got, "\n-b\n") || !strings.Contains(got, "\n+x\n") || !strings.Contains(got, "\n a\n") {
		t.Fatalf("%s", got)
	}
	only := OnlyChanges(got)
	if strings.Contains(only, "\n a\n") || !strings.Contains(only, "\n-b\n") || !strings.Contains(only, "\n+x\n") {
		t.Fatalf("only changes:\n%s", only)
	}
}

func TestUnifiedCollapsesUnchanged(t *testing.T) {
	var old, neu strings.Builder
	for i := range 40 {
		line := "keep\n"
		if i == 20 {
			old.WriteString("before\n")
			neu.WriteString("after\n")
			continue
		}
		old.WriteString(line)
		neu.WriteString(line)
	}
	got := Unified("f", old.String(), neu.String())
	if strings.Count(got, "\n keep\n") > 10 {
		t.Fatalf("unchanged lines not collapsed:\n%s", got)
	}
	if !strings.Contains(got, "\n-before\n") || !strings.Contains(got, "\n+after\n") {
		t.Fatalf("%s", got)
	}
}
