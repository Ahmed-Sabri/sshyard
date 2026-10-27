package policy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnverifiedReadersRequirePermission(t *testing.T) {
	e := NewEngine()
	for _, cmd := range []string{
		"rg --pre=sh x /tmp/report.txt", "rg --pre sh x /tmp/report.txt",
		"rg --hostname-bin sh x /tmp/report.txt", "rg --unknown-option x",
		"uniq /tmp/input /tmp/output", "uniq -c /tmp/input /tmp/output",
		"sort -o/tmp/output /tmp/input", "sort -ro/tmp/output /tmp/input",
		"sort --compress-program=sh /tmp/input", "sort --random-source=/tmp/source /tmp/input",
		"grep -f .env /tmp/report.txt", "grep --file=.env /tmp/report.txt",
		"grep -r needle /tmp/reports", "grep -R needle .", "rg needle .",
		"grep needle .env", "cat /tmp/report.txt", "cat < /tmp/report.txt",
		"find /tmp -exec cat {} \\;", `echo "$DB_PASSWORD"`, `printf %s "$OPENAI_API_KEY"`, `echo "${!PREFIX@}"`, "env", "ps eww", "ps auxe", "printenv", "printenv OPENAI_API_KEY",
		"/tmp/cat --help", "./ls", "unverified-program --help", "date 10271200", "hostname other-host",
	} {
		t.Run(cmd, func(t *testing.T) {
			g := e.Classify(Action{Tool: "run_command", Command: cmd}, Scope{Home: "/home/user"})
			if d := e.Decide(ModeAsk, g, false); d.Outcome != Ask {
				t.Fatalf("auto-ran %q: %+v", cmd, d)
			}
		})
	}
	for _, cmd := range []string{"ls -la /tmp", "df -h", "uname -a", "uptime", "id", "whoami", "ps aux", "pwd", "echo hello", "echo $HOME", "ls | grep hello", "printf hello | sort", "printf hello | uniq", "cat --help", "rm --help"} {
		t.Run("safe "+cmd, func(t *testing.T) {
			g := e.Classify(Action{Tool: "run_command", Command: cmd}, Scope{Home: "/home/user"})
			if d := e.Decide(ModeAsk, g, false); d.Outcome != Allow {
				t.Fatalf("safe command requires approval: %+v", d)
			}
		})
	}
}

func TestRedactNamedAssignments(t *testing.T) {
	for _, input := range []string{
		"DB_PASSWORD=example-sensitive-value next=keep",
		"AWS_SECRET_ACCESS_KEY=example-sensitive-value next=keep",
		"OPENAI_API_KEY='example-sensitive-value with spaces' next=keep",
		`{"OPENAI_API_KEY": "example-sensitive-value with spaces", "count": 3}`,
		`{"AWS_SESSION_TOKEN":"example-sensitive-value","count":3}`,
		`client_secret="example-sensitive-value" next=keep`,
	} {
		got := Redact(input)
		if strings.Contains(got, "example-sensitive-value") || !strings.Contains(got, "[REDACTED]") {
			t.Fatalf("leaked assignment: %q", got)
		}
		if strings.Contains(input, "next=keep") && !strings.Contains(got, "next=keep") {
			t.Fatalf("removed normal field: %q", got)
		}
		if strings.HasPrefix(input, "{") && !json.Valid([]byte(got)) {
			t.Fatalf("redaction invalidated JSON: %q", got)
		}
		if strings.Contains(input, `"count"`) && !strings.Contains(got, `"count"`) {
			t.Fatalf("removed normal JSON field: %q", got)
		}
	}
	for _, input := range []string{"PASSWORD_COUNT=3", "TOKEN_COUNT=3", "next=keep", `{"count":3,"name":"report"}`} {
		if got := Redact(input); got != input {
			t.Fatalf("normal data changed: %q -> %q", input, got)
		}
	}
}
