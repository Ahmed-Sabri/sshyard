package session

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// ErrNoProcs is a server whose processes cannot be read: not Linux (no
// /proc), or output that is not the script's.
var ErrNoProcs = errors.New("session: the server's processes could not be read")

// procsScript prints, from Linux's /proc, what ProcSample holds: the page
// size (K), the CPU lines of /proc/stat (C), the memory (M), the users by
// uid (U), then for each process (P) its uid, the bytes it read and wrote
// from disk ("-" where /proc/<pid>/io cannot be read, a process of another
// user) and its stat line. It reads the files with sh's own read, so that
// it forks a few times, not once per process.
const procsScript = `[ -r /proc/stat ] || exit 3
echo "K $(getconf PAGESIZE 2>/dev/null || echo 4096)"
grep "^cpu" /proc/stat | sed "s/^/C /"
grep "^MemTotal:" /proc/meminfo | sed "s/^/M /"
awk -F: "{ print \"U\", \$3, \$1 }" /etc/passwd 2>/dev/null
for d in /proc/[0-9]*; do
  { read -r s < "$d/stat"; } 2>/dev/null || continue
  u=-; r=-; w=-
  { while read -r k a b; do [ "$k" = Uid: ] && { u=$a; break; }; done < "$d/status"; } 2>/dev/null
  { while read -r k v; do case $k in read_bytes:) r=$v;; write_bytes:) w=$v;; esac; done < "$d/io"; } 2>/dev/null
  echo "P $u $r $w $s"
done`

// ProcsCommand is the remote command reading the processes once.
func ProcsCommand() string { return "sh -c " + shellQuote(procsScript) }

// ProcRaw is one process as one reading of /proc saw it.
type ProcRaw struct {
	PID   int
	Name  string
	UID   string
	Ticks uint64 // user and system CPU time, in clock ticks
	RSS   uint64 // resident pages
	// Read and Write are the bytes the process read and wrote from disk,
	// IOOK whether they could be read.
	Read, Write uint64
	IOOK        bool
}

// ProcSample is one reading of a server's processes.
type ProcSample struct {
	At       time.Time
	CPUTotal uint64 // all CPUs' time, in clock ticks
	CPUs     int
	PageKB   uint64
	MemKB    uint64
	Users    map[string]string // names by uid
	Procs    map[int]ProcRaw
}

// ParseProcs reads the output of ProcsCommand.
func ParseProcs(out []byte) (ProcSample, error) {
	s := ProcSample{PageKB: 4, Users: map[string]string{}, Procs: map[int]ProcRaw{}}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	cpu := false
	for sc.Scan() {
		tag, rest, _ := strings.Cut(sc.Text(), " ")
		switch tag {
		case "K":
			if n, err := strconv.ParseUint(strings.TrimSpace(rest), 10, 64); err == nil && n >= 1024 {
				s.PageKB = n / 1024
			}
		case "C":
			f := strings.Fields(rest)
			if len(f) < 5 {
				continue
			}
			if f[0] != "cpu" {
				s.CPUs++
				continue
			}
			for _, v := range f[1:] {
				n, _ := strconv.ParseUint(v, 10, 64)
				s.CPUTotal += n
			}
			cpu = true
		case "M":
			if f := strings.Fields(rest); len(f) >= 2 {
				s.MemKB, _ = strconv.ParseUint(f[1], 10, 64)
			}
		case "U":
			if uid, name, ok := strings.Cut(rest, " "); ok {
				s.Users[uid] = name
			}
		case "P":
			if p, ok := parseProc(rest); ok {
				s.Procs[p.PID] = p
			}
		}
	}
	if !cpu {
		return ProcSample{}, ErrNoProcs
	}
	s.CPUs = max(s.CPUs, 1)
	return s, nil
}

// parseProc reads a P line: uid, read and write bytes, then the stat line
// "pid (comm) state ppid …", whose comm may hold spaces and parentheses.
func parseProc(rest string) (ProcRaw, bool) {
	f := strings.SplitN(rest, " ", 4)
	if len(f) < 4 {
		return ProcRaw{}, false
	}
	p := ProcRaw{UID: f[0]}
	r, err1 := strconv.ParseUint(f[1], 10, 64)
	w, err2 := strconv.ParseUint(f[2], 10, 64)
	if err1 == nil && err2 == nil {
		p.Read, p.Write, p.IOOK = r, w, true
	}
	stat := f[3]
	open, close := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
	if open < 0 || close < open {
		return ProcRaw{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(stat[:open]))
	if err != nil {
		return ProcRaw{}, false
	}
	p.PID, p.Name = pid, stat[open+1:close]
	// After the comm: state (3), … utime (14), stime (15), … rss (24).
	fs := strings.Fields(stat[close+1:])
	if len(fs) < 22 {
		return ProcRaw{}, false
	}
	ut, _ := strconv.ParseUint(fs[11], 10, 64)
	st, _ := strconv.ParseUint(fs[12], 10, 64)
	p.Ticks = ut + st
	p.RSS, _ = strconv.ParseUint(fs[21], 10, 64)
	return p, true
}

// ReadProcs reads the processes of the server over client once.
func ReadProcs(ctx context.Context, client *ssh.Client) (ProcSample, error) {
	var out bytes.Buffer
	if err := Exec(ctx, client, ProcsCommand(), &out, nil); err != nil {
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			return ProcSample{}, ErrNoProcs
		}
		return ProcSample{}, err
	}
	s, err := ParseProcs(out.Bytes())
	s.At = time.Now()
	return s, err
}

// Proc is a process as two readings show it.
type Proc struct {
	PID        int
	Name, User string
	// CPU is the share of one CPU it took between the readings, in
	// percent, as top shows it (up to 100 per CPU); CPUOK is false for a
	// process the first reading lacked.
	CPU    float64
	CPUOK  bool
	MemKB  uint64
	MemPct float64
	// Disk is the bytes it read and wrote from disk a second between the
	// readings, DiskOK whether that is known.
	Disk   float64
	DiskOK bool
}

// ProcDelta is the processes of cur, with what they used since prev.
func ProcDelta(prev, cur ProcSample) []Proc {
	sec := cur.At.Sub(prev.At).Seconds()
	var perCPU float64
	if cur.CPUTotal > prev.CPUTotal && prev.CPUTotal > 0 {
		perCPU = float64(cur.CPUTotal-prev.CPUTotal) / float64(cur.CPUs)
	}
	out := make([]Proc, 0, len(cur.Procs))
	for pid, p := range cur.Procs {
		q := Proc{PID: pid, Name: p.Name, User: cur.Users[p.UID], MemKB: p.RSS * cur.PageKB}
		if q.User == "" {
			q.User = p.UID
		}
		if cur.MemKB > 0 {
			q.MemPct = float64(q.MemKB) / float64(cur.MemKB) * 100
		}
		if o, ok := prev.Procs[pid]; ok && o.Name == p.Name {
			if perCPU > 0 && p.Ticks >= o.Ticks {
				q.CPU, q.CPUOK = float64(p.Ticks-o.Ticks)/perCPU*100, true
			}
			if sec > 0 && p.IOOK && o.IOOK && p.Read >= o.Read && p.Write >= o.Write {
				q.Disk, q.DiskOK = float64(p.Read-o.Read+p.Write-o.Write)/sec, true
			}
		}
		out = append(out, q)
	}
	return out
}
