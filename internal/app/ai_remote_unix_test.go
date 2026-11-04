//go:build !windows

package app

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/llm"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

func TestAskModeRunsReadOnlyAndPromptsForWrite(t *testing.T) {
	a, tt, _, h := bashHost(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(root, "nginx.conf")
	a.newModel = scripted(toolCall("c1", "run_command", `{"command":"df -h"}`), toolCall("w1", "write_file", writeArgs(t, conf, "worker_processes 1;\n")))
	id := openAIPane(t, a, h.ID)
	tt.Frame()
	a.SendAI(id, "check the disk, then edit nginx")
	waitFor(t, tt, "write confirmation", func() bool { return tt.HasText("Allow once") })
	if tt.HasText("Allow similar this session") {
		t.Fatal("a file write offered allow-similar")
	}
	var df agent.ToolCard
	for _, it := range a.conns[id].ai.items {
		if it.tool.Command == "df -h" {
			df = it.tool
		}
	}
	if df.Phase != "done" || df.Verdict != "auto" || df.Exit != 0 || !strings.Contains(df.Output, "Filesystem") {
		t.Fatalf("df card %+v", df)
	}
	pend := a.conns[id].ai.pending
	if pend == nil || pend.prompt.Action.Tool != "write_file" || !pend.prompt.Decision.Diff || pend.prompt.Decision.Similar {
		t.Fatalf("prompt %+v", pend)
	}
	a.StopAI(id)
	waitFor(t, tt, "stopped", func() bool { return !a.conns[id].ai.running })
}

func TestWriteConfirmAndRefuseChangedFile(t *testing.T) {
	a, tt, _, h := bashHost(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(root, "nginx.conf")
	if err := os.WriteFile(conf, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.newModel = scripted(
		toolCall("w1", "write_file", writeArgs(t, conf, "worker_processes 1;\n")),
		llm.Message{Role: "assistant", Content: "wrote"},
		toolCall("w2", "write_file", writeArgs(t, conf, "worker_processes 2;\n")),
		llm.Message{Role: "assistant", Content: "stopped"},
	)
	id := openAIPane(t, a, h.ID)
	tt.Frame()
	a.SendAI(id, "edit nginx")
	waitFor(t, tt, "diff", func() bool { return tt.HasText("Allow once") && tt.HasText("worker_processes") })
	mustClick(t, tt, "Allow once")
	waitFor(t, tt, "written", func() bool {
		b, err := os.ReadFile(conf)
		return err == nil && string(b) == "worker_processes 1;\n" && !a.conns[id].ai.running
	})

	a.SendAI(id, "edit nginx again")
	waitFor(t, tt, "second diff", func() bool { return tt.HasText("Allow once") })
	if err := os.WriteFile(conf, []byte("raced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustClick(t, tt, "Allow once")
	waitFor(t, tt, "refused", func() bool {
		return paneHas(a, id, "the file changed on the server; read it again before writing") && !a.conns[id].ai.running
	})
	b, err := os.ReadFile(conf)
	if err != nil || string(b) != "raced\n" {
		t.Fatalf("file %q err %v", b, err)
	}
	var changed agent.ToolCard
	for _, it := range a.conns[id].ai.items {
		if it.tool.ID == "w2" {
			changed = it.tool
		}
	}
	if changed.Phase != "done" || changed.Exit != 1 || !strings.Contains(changed.Output, "the file changed on the server; read it again before writing") {
		t.Fatalf("changed file card %+v", changed)
	}
}

func TestStopKillsRemoteSleep(t *testing.T) {
	a, tt, srv, h := bashHost(t)
	var mu sync.Mutex
	var pids []int
	srv.Exec = trackExec(&mu, &pids)
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, pid := range pids {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	a.newModel = scripted(toolCall("c1", "run_command", `{"command":"sleep 600"}`))
	id := openAIPane(t, a, h.ID)
	tt.Frame()
	a.SendAI(id, "sleep")
	waitFor(t, tt, "sleep confirmation", func() bool { return tt.HasText("Allow once") })
	mustClick(t, tt, "Allow once")
	var pid int
	waitFor(t, tt, "sleep started", func() bool {
		mu.Lock()
		defer mu.Unlock()
		if len(pids) == 0 {
			return false
		}
		pid = pids[0]
		return syscall.Kill(pid, 0) == nil
	})
	tt.Key(stopMods, ui.KeyPeriod)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			auditDeadline := time.Now().Add(time.Second)
			for {
				body, ok := auditCancel(a.p.DataDir)
				if ok {
					return
				}
				if time.Now().After(auditDeadline) {
					t.Fatalf("process gone but audit missing the cancel: %s", body)
				}
				time.Sleep(20 * time.Millisecond)
				tt.Frame()
			}
		}
		time.Sleep(20 * time.Millisecond)
		tt.Frame()
	}
	t.Fatal("sleep 600 still running 2s after Stop")
}

func TestFullModeAuditDecider(t *testing.T) {
	a, tt, _, h := bashHost(t)
	s := a.Settings()
	s.AIDefaultMode = "full"
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.newModel = scripted(
		toolCall("c1", "run_command", `{"command":"echo hi"}`),
		llm.Message{Role: "assistant", Content: "done"},
	)
	id := openAIPane(t, a, h.ID)
	tt.Frame()
	a.SendAI(id, "say hi")
	waitFor(t, tt, "echo finished", func() bool {
		return !a.conns[id].ai.running && paneHas(a, id, "hi")
	})
	var card agent.ToolCard
	for _, it := range a.conns[id].ai.items {
		if it.tool.Command == "echo hi" {
			card = it.tool
		}
	}
	if card.Verdict != "full" || card.Exit != 0 || tt.HasText("Allow once") {
		t.Fatalf("card %+v", card)
	}
	body := readAudit(t, a.p.DataDir)
	if !strings.Contains(body, `"decider":"full"`) || !strings.Contains(body, `"output_bytes":3`) {
		t.Fatalf("audit %s", body)
	}
}

func TestAICommandConcurrency(t *testing.T) {
	a, tt, srv, h := bashHost(t)
	var cur, maxn, started atomic.Int32
	srv.Exec = func(ch ssh.Channel, line string) int {
		if strings.Contains(line, "kill -TERM") || strings.Contains(line, "STILL_ALIVE") {
			return bashExec(ch, line)
		}
		n := cur.Add(1)
		started.Add(1)
		for {
			old := maxn.Load()
			if n <= old || maxn.CompareAndSwap(old, n) {
				break
			}
		}
		defer cur.Add(-1)
		return bashExec(ch, line)
	}
	a.newModel = scripted(toolCall("c1", "run_command", `{"command":"tail -f /dev/null"}`))
	id1 := openAIPane(t, a, h.ID)
	id2, err := a.OpenAI(h.ID, openAt{pane: id1, edge: layout.Right}, 0)
	if err != nil {
		t.Fatal(err)
	}
	id3, err := a.OpenAI(h.ID, openAt{pane: id2, edge: layout.Bottom}, 0)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	allowCommand := func(id int) {
		waitFor(t, tt, "command permission request", func() bool { return a.conns[id].ai.pending != nil })
		prompt := a.conns[id].ai.pending.prompt
		if prompt.Action.Tool != "run_command" || prompt.Action.Command != "tail -f /dev/null" || prompt.Decision.Mode != policy.ModeAsk || prompt.Decision.Outcome != policy.Ask {
			t.Fatalf("unexpected permission request: %+v", prompt)
		}
		a.answerAI(a.conns[id], policy.AllowOnce)
	}
	a.SendAI(id1, "hold 1")
	allowCommand(id1)
	waitFor(t, tt, "first command", func() bool { return started.Load() >= 1 })
	a.SendAI(id2, "hold 2")
	allowCommand(id2)
	waitFor(t, tt, "second command", func() bool { return started.Load() >= 2 })
	a.SendAI(id3, "hold 3")
	allowCommand(id3)
	time.Sleep(time.Second)
	tt.Frame()
	if started.Load() != 2 || maxn.Load() > 2 {
		t.Fatalf("before stop started %d max %d cur %d", started.Load(), maxn.Load(), cur.Load())
	}
	a.StopAI(id1)
	waitFor(t, tt, "third command", func() bool { return started.Load() >= 3 })
	if maxn.Load() > 2 {
		t.Fatalf("max %d", maxn.Load())
	}
	a.StopAllAI()
	waitFor(t, tt, "all stopped", func() bool {
		return !a.conns[id1].ai.running && !a.conns[id2].ai.running && !a.conns[id3].ai.running
	})
}

func bashHost(t *testing.T) (*App, *ui.Tester, *sshtest.Server, assets.Host) {
	t.Helper()
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	srv.Exec = bashExec
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "web", "secret")
	if err := a.known.Add(h.Addr(), srv.HostKey.PublicKey()); err != nil {
		t.Fatal(err)
	}
	consentHost(t, a, h.ID)
	t.Cleanup(func() {
		a.StopAllAI()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			a.drain()
			busy := false
			for _, c := range a.conns {
				if c.ai != nil && c.ai.running {
					busy = true
				}
			}
			if !busy {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	return a, tt, srv, h
}

func openAIPane(t *testing.T, a *App, hostID string) int {
	t.Helper()
	id, err := a.OpenAI(hostID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func scripted(calls ...llm.Message) func(*conn) agent.Streamer {
	return func(*conn) agent.Streamer { return &replyModel{replies: calls} }
}

func toolCall(id, name, args string) llm.Message {
	return llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: args}}}
}

func writeArgs(t *testing.T, path, content string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"path": path, "content": content, "reason": "edit"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func auditCancel(dir string) (string, bool) {
	matches, err := filepath.Glob(filepath.Join(dir, "ai-audit", "*.jsonl"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, name := range matches {
		body, err := os.ReadFile(name)
		if err != nil {
			return "", false
		}
		b.Write(body)
	}
	body := b.String()
	ok := strings.Contains(body, `"command":"sleep 600"`) &&
		strings.Contains(body, `"verdict":"cancelled"`) &&
		strings.Contains(body, `"exit_code":-1`) &&
		strings.Contains(body, `"duration_ms":`) &&
		strings.Contains(body, `"output_bytes":`)
	return body, ok
}

func readAudit(t *testing.T, dir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "ai-audit", "*.jsonl"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("audit log: %v files %v", err, matches)
	}
	var b strings.Builder
	for _, name := range matches {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(body)
	}
	return b.String()
}

func trackExec(mu *sync.Mutex, pids *[]int) func(ssh.Channel, string) int {
	return func(ch ssh.Channel, line string) int {
		if strings.Contains(line, "kill -TERM") || strings.Contains(line, "STILL_ALIVE") {
			return bashExec(ch, line)
		}
		return bashTrack(ch, line, func(pid int) {
			mu.Lock()
			*pids = append(*pids, pid)
			mu.Unlock()
		})
	}
}

func bashExec(ch ssh.Channel, line string) int { return bashTrack(ch, line, nil) }

func bashTrack(ch ssh.Channel, line string, pid func(int)) int {
	cmd := exec.Command("/bin/bash", "-c", line)
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()
	in, err := cmd.StdinPipe()
	if err != nil {
		return 127
	}
	if err := cmd.Start(); err != nil {
		return 127
	}
	if pid != nil && cmd.Process != nil {
		pid(cmd.Process.Pid)
	}
	go func() { io.Copy(in, ch); in.Close() }()
	err = cmd.Wait()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 127
}
