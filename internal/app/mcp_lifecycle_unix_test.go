//go:build !windows

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/ai/audit"
	"github.com/Licoy/sshyard/internal/assets"
)

func connectMCPTest(t *testing.T, a *App, h assets.Host) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"host_id": h.ID})
	text, err := a.mcpConnect(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		ID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out.ID
}

func waitMCPFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("command did not create %s", path)
}

func TestMCPCancelSharedCommand(t *testing.T) {
	for _, stop := range []string{"disconnect", "stop", "rotate"} {
		t.Run(stop, func(t *testing.T) {
			a, _, _, h := bashHost(t)
			if stop == "rotate" {
				s := a.Settings()
				s.MCPPort = freePort(t)
				if err := a.ApplySettings(s); err != nil {
					t.Fatal(err)
				}
				if err := a.setMCP(true); err != nil {
					t.Fatal(err)
				}
			}
			first, second := connectMCPTest(t, a, h), connectMCPTest(t, a, h)
			shared, err := a.mcpSession(second)
			if err != nil {
				t.Fatal(err)
			}
			// An independent pane keeps this transport open even when all MCP sessions close.
			client, release, err := a.pool.Acquire(context.Background(), a.poolKey(h), a.hostConfig(h, nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if client != shared.client {
				t.Fatal("test did not share transport")
			}
			dir := t.TempDir()
			started := filepath.Join(dir, "started")
			escaped := filepath.Join(dir, "escaped")
			raw, _ := json.Marshal(map[string]string{"session_id": first, "command": fmt.Sprintf("touch %q; sleep 1; touch %q", started, escaped)})
			done := make(chan error, 1)
			aud := audit.New(a.p.DataDir)
			go func() { _, err := a.mcpRun(context.Background(), raw, aud); done <- err }()
			waitMCPFile(t, started)
			switch stop {
			case "disconnect":
				disconnect, _ := json.Marshal(map[string]string{"session_id": first})
				if _, err := a.mcpDisconnect(context.Background(), disconnect); err != nil {
					t.Fatal(err)
				}
			case "stop":
				a.stopMCP()
			case "rotate":
				if err := a.newMCPToken(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "canceled") {
					t.Fatalf("cancel: %v", err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("command not cancelled")
			}
			time.Sleep(1100 * time.Millisecond)
			if _, err := os.Stat(escaped); !os.IsNotExist(err) {
				t.Fatalf("cancelled command survived: %v", err)
			}
			sess, err := client.NewSession()
			if err != nil {
				t.Fatal("shared transport closed:", err)
			}
			defer sess.Close()
			if err := sess.Run("true"); err != nil {
				t.Fatal(err)
			}
			recs, err := aud.Read(audit.Filter{})
			if err != nil || len(recs) != 2 || recs[1].Verdict != "cancelled" || recs[1].ExitCode == nil || *recs[1].ExitCode != -1 {
				t.Fatalf("audit: %+v %v", recs, err)
			}
			a.stopMCP()
			a.waitMCP()
		})
	}
}

func TestMCPConcurrentSessionLimit(t *testing.T) {
	a, _, _, h := bashHost(t)
	raw, _ := json.Marshal(map[string]string{"host_id": h.ID})
	start := make(chan struct{})
	results := make(chan error, 48)
	var wg sync.WaitGroup
	for range 48 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, err := a.mcpConnect(context.Background(), raw); results <- err }()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != mcpMaxSessions || len(a.mcpSessionList()) != mcpMaxSessions {
		t.Fatalf("limit: successes=%d sessions=%d", successes, len(a.mcpSessionList()))
	}
	a.stopMCP()
	a.waitMCP()
	if a.mcp.pending != 0 {
		t.Fatalf("pending reservation leaked: %d", a.mcp.pending)
	}
	if id := connectMCPTest(t, a, h); id == "" {
		t.Fatal("limit not released")
	}
	a.stopMCP()
	a.waitMCP()
}

func TestMCPAuditFailurePreventsCommand(t *testing.T) {
	a, _, _, h := bashHost(t)
	id := connectMCPTest(t, a, h)
	defer func() { a.stopMCP(); a.waitMCP() }()
	for _, monthBlocked := range []bool{false, true} {
		dir := t.TempDir()
		logDir := filepath.Join(dir, "ai-audit")
		if monthBlocked {
			if err := os.MkdirAll(filepath.Join(logDir, time.Now().Format("2006-01")+".jsonl"), 0700); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(logDir, []byte("blocked"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		marker := filepath.Join(dir, "executed")
		raw, _ := json.Marshal(map[string]string{"session_id": id, "command": fmt.Sprintf("touch %q", marker)})
		_, err := a.mcpRun(context.Background(), raw, audit.New(dir))
		if err == nil || !strings.Contains(err.Error(), "not executed") {
			t.Fatalf("audit failure: %v", err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("command executed without audit: %v", err)
		}
	}
}

func TestMCPAuditFailureAfterExecutionIsExplicit(t *testing.T) {
	a, _, _, h := bashHost(t)
	id := connectMCPTest(t, a, h)
	defer func() { a.stopMCP(); a.waitMCP() }()
	dir := t.TempDir()
	month := filepath.Join(dir, "ai-audit", time.Now().Format("2006-01")+".jsonl")
	marker := filepath.Join(dir, "executed")
	raw, _ := json.Marshal(map[string]string{"session_id": id, "command": fmt.Sprintf("touch %q; rm %q; mkdir %q", marker, month, month)})
	_, err := a.mcpRun(context.Background(), raw, audit.New(dir))
	if err == nil || !strings.Contains(err.Error(), "execution finished but audit recording failed") || !strings.Contains(err.Error(), "do not retry") {
		t.Fatalf("missing post-execution failure: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("command never executed: %v", err)
	}
}

func TestMCPStopCancelsPendingConnect(t *testing.T) {
	a, _, _, h := bashHost(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	h.Port = ln.Addr().(*net.TCPAddr).Port
	if _, err := a.store.Update(h); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"host_id": h.ID})
	done := make(chan error, 1)
	go func() { _, err := a.mcpConnect(context.Background(), raw); done <- err }()
	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("dial never began")
	}
	a.stopMCP()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("pending connection survived stop")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dial not cancelled")
	}
	a.waitMCP()
	if len(a.mcpSessionList()) != 0 || a.mcp.pending != 0 {
		t.Fatalf("late session or reservation after stop: %d %d", len(a.mcpSessionList()), a.mcp.pending)
	}
}

func TestMCPFailedConnectReleasesReservation(t *testing.T) {
	a, _, _, h := bashHost(t)
	good := h
	h.Port = freePort(t)
	if _, err := a.store.Update(h); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"host_id": h.ID})
	for range mcpMaxSessions + 1 {
		if _, err := a.mcpConnect(context.Background(), raw); err == nil {
			t.Fatal("connection unexpectedly succeeded")
		}
		if a.mcp.pending != 0 {
			t.Fatalf("failed dial leaked reservation: %d", a.mcp.pending)
		}
	}
	if _, err := a.store.Update(good); err != nil {
		t.Fatal(err)
	}
	connectMCPTest(t, a, good)
	a.stopMCP()
	a.waitMCP()
}
