package session

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestProcsScript runs the script on a /proc of its own and reads what it
// prints: the CPUs, the memory, and each process with its user, its
// times, its pages and, where it can be read, its disk I/O.
func TestProcsScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	root := t.TempDir()
	write := func(name, s string) {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("stat", "cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 50 0 50 400 0 0 0 0 0 0\ncpu1 50 0 50 400 0 0 0 0 0 0\nintr 1 2 3\n")
	write("meminfo", "MemTotal:        2048000 kB\nMemFree: 1 kB\n")
	tail := " 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0"
	write("10/stat", "10 (nginx: worker (x)) S 1 10 10 0 -1 4194560 0 0 0 0 30 12 0 0 20 0 1 0 100 1000 256"+tail+"\n")
	write("10/status", "Name:\tnginx\nUmask:\t0022\nUid:\t33\t33\t33\t33\nGid:\t33\n")
	write("10/io", "rchar: 1\nwchar: 2\nread_bytes: 4096\nwrite_bytes: 8192\n")
	write("20/stat", "20 (sshd) S 1 20 20 0 -1 4194560 0 0 0 0 5 5 0 0 20 0 1 0 100 1000 512"+tail+"\n")
	write("20/status", "Name:\tsshd\nUid:\t0\t0\t0\t0\n")
	// 20 has no io file: another user's process.
	script := strings.ReplaceAll(procsScript, "/proc", root)
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	s, err := ParseProcs(out)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if s.CPUs != 2 || s.CPUTotal != 1000 || s.MemKB != 2048000 || s.PageKB == 0 {
		t.Errorf("sample %+v", s)
	}
	ng, ok := s.Procs[10]
	if !ok || ng.Name != "nginx: worker (x)" || ng.UID != "33" || ng.Ticks != 42 || ng.RSS != 256 || !ng.IOOK || ng.Read != 4096 || ng.Write != 8192 {
		t.Errorf("nginx %+v", ng)
	}
	if sd := s.Procs[20]; sd.Name != "sshd" || sd.UID != "0" || sd.IOOK || sd.RSS != 512 {
		t.Errorf("sshd %+v", sd)
	}
	if s.Users["0"] == "" && len(s.Users) > 0 {
		t.Errorf("users %v", s.Users)
	}
}

func TestParseProcsNotLinux(t *testing.T) {
	if _, err := ParseProcs([]byte("K 4096\n")); err != ErrNoProcs {
		t.Errorf("no CPU line: %v", err)
	}
}

// TestProcDelta takes what each process used between two readings: CPU
// as top shows it, per CPU, and disk I/O a second; a process new in the
// second reading, or a pid taken by another program, has neither.
func TestProcDelta(t *testing.T) {
	t0 := time.Unix(1000, 0)
	prev := ProcSample{At: t0, CPUTotal: 1000, CPUs: 2, PageKB: 4, MemKB: 4000, Users: map[string]string{"0": "root"}, Procs: map[int]ProcRaw{
		1: {PID: 1, Name: "busy", UID: "0", Ticks: 100, RSS: 100, Read: 0, Write: 0, IOOK: true},
		2: {PID: 2, Name: "old", UID: "0", Ticks: 10},
	}}
	cur := ProcSample{At: t0.Add(2 * time.Second), CPUTotal: 1400, CPUs: 2, PageKB: 4, MemKB: 4000, Users: prev.Users, Procs: map[int]ProcRaw{
		1: {PID: 1, Name: "busy", UID: "0", Ticks: 300, RSS: 100, Read: 2048, Write: 2048, IOOK: true},
		2: {PID: 2, Name: "new", UID: "7", Ticks: 50},
		3: {PID: 3, Name: "born", UID: "0", Ticks: 1},
	}}
	got := map[int]Proc{}
	for _, p := range ProcDelta(prev, cur) {
		got[p.PID] = p
	}
	b := got[1]
	// 200 ticks of the 200 each CPU had: one whole CPU.
	if !b.CPUOK || math.Abs(b.CPU-100) > 0.01 || b.MemKB != 400 || math.Abs(b.MemPct-10) > 0.01 || !b.DiskOK || b.Disk != 2048 || b.User != "root" {
		t.Errorf("busy %+v", b)
	}
	if p := got[2]; p.CPUOK || p.DiskOK || p.User != "7" {
		t.Errorf("reused pid %+v", p)
	}
	if p := got[3]; p.CPUOK {
		t.Errorf("new %+v", p)
	}
}
