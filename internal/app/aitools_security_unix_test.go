//go:build !windows

package app

import (
	"strings"
	"testing"
	"time"
)

func TestHostCommandOutputLimitCancelsProducer(t *testing.T) {
	a, _, _, host := bashHost(t)
	tools := newHostTools(a, &conn{host: host})
	defer tools.end()
	if err := tools.connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	text, exit, err := tools.command(t.Context(), "yes output", "")
	if err != nil || exit != 1 || !strings.HasPrefix(text, "ai.command_output_limit\n") || len(text) > 1<<20 {
		t.Fatalf("output len=%d exit=%d err=%v", len(text), exit, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("output producer did not stop promptly")
	}
	text, exit, err = tools.command(t.Context(), "printf healthy", "")
	if err != nil || exit != 0 || text != "healthy" {
		t.Fatalf("connection unusable after output limit: %q %d %v", text, exit, err)
	}
}
