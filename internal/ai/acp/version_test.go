package acp

import "testing"

func TestProtocolVersionIsOne(t *testing.T) {
	if ProtocolVersion != 1 {
		t.Fatalf("protocol version %d, want 1", ProtocolVersion)
	}
}
