package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestCommandOutputLimitCancelsAndBoundsBothStreams(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out := &commandOutput{remaining: 1024, cancel: cancel}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for range 10 {
				n, err := out.Write([]byte(strings.Repeat("x", 512)))
				if err != nil || n != 512 {
					t.Errorf("Write=%d %v", n, err)
				}
			}
		})
	}
	wg.Wait()
	text, limited := out.result()
	if len(text) != 1024 || !limited || ctx.Err() != context.Canceled {
		t.Fatalf("len=%d limited=%v context=%v", len(text), limited, ctx.Err())
	}
}

func TestCommandOutputBelowLimitRemainsComplete(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out := &commandOutput{remaining: 1024, cancel: cancel}
	out.Write([]byte("stdout\n"))
	out.Write([]byte("stderr\n"))
	text, limited := out.result()
	if text != "stdout\nstderr\n" || limited || ctx.Err() != nil {
		t.Fatalf("result=%q limited=%v context=%v", text, limited, ctx.Err())
	}
}

func TestCommandOutputLimitRetainsCleanupFailures(t *testing.T) {
	cleanup := fmt.Errorf("%w: failed to terminate remote process group", context.Canceled)
	text, code, err := commandLimitResult(strings.Repeat("x", (1<<20)-4096), cleanup)
	if code != 1 || err == nil || errors.Is(err, context.Canceled) || !strings.HasPrefix(text, "ai.command_output_limit\n") || !strings.Contains(text, "failed to terminate remote process group") || len(text) > 1<<20 {
		t.Fatalf("limit result: code=%d err=%v len=%d", code, err, len(text))
	}
	text, code, err = commandLimitResult("partial", context.Canceled)
	if code != 1 || err != nil || text != "ai.command_output_limit\npartial" {
		t.Fatalf("expected limit cancellation: %q %d %v", text, code, err)
	}
}
