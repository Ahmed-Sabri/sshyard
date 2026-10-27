package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// AuditRecord is one JSONL audit line. The audit package writes it.
// File contents, command output, and secrets are never fields here.
// ExitCode is nil when the action did not run.
type AuditRecord struct {
	Time         time.Time `json:"time"`
	SessionID    string    `json:"session_id"`
	PaneID       string    `json:"pane_id"`
	Agent        string    `json:"agent"`
	Host         string    `json:"host"`
	Mode         string    `json:"mode"`
	Where        string    `json:"where"`
	Category     string    `json:"category"`
	Risk         string    `json:"risk"`
	Command      string    `json:"command"`
	DiffHash     string    `json:"diff_hash"`
	DiffLines    DiffLines `json:"diff_lines"`
	Verdict      string    `json:"verdict"`
	Decider      string    `json:"decider"`
	ReviewReason string    `json:"review_reason"`
	ExitCode     *int      `json:"exit_code"`
	DurationMS   int64     `json:"duration_ms"`
	OutputBytes  int64     `json:"output_bytes"`
}

// DiffLines counts added and removed lines in a unified diff.
// The diff text itself is not stored.
type DiffLines struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// Auditor appends one record. A nil auditor skips the log.
// Record must be safe for concurrent use.
type Auditor interface {
	Record(AuditRecord) error
}

// AuditPreparer is optional. The gate calls Prepare before it runs an
// action and refuses to run when the log cannot be opened.
type AuditPreparer interface {
	Prepare() error
}

func diffStats(diff string) (hash string, lines DiffLines) {
	if diff == "" {
		return "", DiffLines{}
	}
	sum := sha256.Sum256([]byte(diff))
	hash = hex.EncodeToString(sum[:])
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			lines.Added++
		case strings.HasPrefix(line, "-"):
			lines.Removed++
		}
	}
	return hash, lines
}

func diffSummary(diff string) string {
	if diff == "" {
		return ""
	}
	hash, lines := diffStats(diff)
	return "added " + itoaDec(lines.Added) + ", removed " + itoaDec(lines.Removed) + ", sha256 " + hash
}

func itoaDec(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func verdictOf(v Verdict) (verdict, decider string) {
	if v.Run {
		switch v.By {
		case ByReviewer:
			return "reviewer-allow", "reviewer"
		case ByUser:
			return "user-allow", "user"
		case ByFull:
			return "full", "full"
		default:
			return "auto", "rule"
		}
	}
	switch v.Answer {
	case TimedOut:
		return "timeout", "user"
	case Cancelled:
		return "cancelled", "user"
	default:
		return "reject", "user"
	}
}
