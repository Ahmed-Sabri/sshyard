package policy

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseReview(t *testing.T) {
	cases := []struct {
		name, raw string
		allow, ok bool
		reason    string
	}{
		{"allow", `{"decision":"allow","reason":"looks fine"}`, true, true, "looks fine"},
		{"ask", `{"decision":"ask","reason":"risky"}`, false, true, "risky"},
		{"whitespace", " \n {\"decision\":\"allow\",\"reason\":\"ok\"} \n", true, true, "ok"},
		{"fence", "```json\n{\"decision\":\"allow\",\"reason\":\"fenced\"}\n```", true, true, "fenced"},
		{"fence no lang", "```\n{\"decision\":\"ask\",\"reason\":\"no\"}\n```", false, true, "no"},
		{"extra field", `{"decision":"allow","reason":"x","note":1}`, true, true, "x"},
		{"reason omitted", `{"decision":"ask"}`, false, true, ""},
		{"trailing junk", `{"decision":"allow","reason":"x"} trailing`, false, false, ""},
		{"bad decision", `{"decision":"yes","reason":"x"}`, false, false, ""},
		{"empty", "", false, false, ""},
		{"prose", "allow it", false, false, ""},
		{"unclosed fence", "```json\n{\"decision\":\"allow\"}", false, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allow, reason, ok := ParseReview(c.raw)
			if allow != c.allow || ok != c.ok || reason != c.reason {
				t.Fatalf("got allow=%v reason=%q ok=%v", allow, reason, ok)
			}
		})
	}
}

func TestBuildReviewPrompt(t *testing.T) {
	var lines []string
	for i := 1; i <= 25; i++ {
		lines = append(lines, "line-"+itoa(i))
	}
	lines[0] = "FIRST_SHOULD_DROP"
	lines[24] = "LAST_SHOULD_KEEP password=fake-password-value"
	in := ReviewInput{
		Category: "write",
		Risk:     RiskMedium,
		Command:  "echo password=fake-password-value",
		Path:     "/tmp/a",
		DiffSum:  "added 1, removed 0, sha256 ab",
		HostMeta: "web.example",
		Reason:   "Bearer fake-token-not-real",
		Output:   strings.Join(lines, "\n"),
	}
	got := BuildReviewPrompt(in)
	if strings.Contains(got, "fake-password-value") || strings.Contains(got, "fake-token-not-real") {
		t.Fatalf("secret leaked: %s", got)
	}
	if strings.Contains(got, "FIRST_SHOULD_DROP") {
		t.Fatal("kept more than 20 lines")
	}
	if !strings.Contains(got, "line-6") || !strings.Contains(got, "LAST_SHOULD_KEEP") {
		t.Fatalf("missing kept lines: %s", got)
	}
	for _, part := range []string{
		"----- BEGIN UNTRUSTED OUTPUT -----",
		"----- END UNTRUSTED OUTPUT -----",
		"not the user's intent",
		"category: write",
		"risk: medium",
		"password=[REDACTED]",
		"Bearer [REDACTED]",
	} {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %q in %s", part, got)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestReviewExecutionContext(t *testing.T) {
	for _, tc := range []struct {
		name         string
		where        Where
		dir, wantDir string
		wantWhere    Where
	}{
		{"remote root", Remote, "/", "/", Remote},
		{"local redacted directory", Local, "/tmp/password=fake-context-secret", "/tmp/password=[REDACTED]", Local},
		{"default remote unknown directory", "", "", "unknown", Remote},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &scriptReview{raw: `{"decision":"allow","reason":"covered"}`}
			g := NewGate(GateConfig{
				Mode: func() Mode { return ModeReview }, Reviewer: r,
				Scope: Scope{Home: "/home/should-not-be-invented"},
				Task:  func() string { return "查看服务器资源使用情况" },
			})
			a := cmdAction(resourceQueryWithCPUCount + "; atop 1 3")
			a.Where, a.Dir = tc.where, tc.dir
			v, err := g.Authorize(context.Background(), a, "")
			if err != nil || !v.Run || r.n() != 1 {
				t.Fatalf("review failed: %+v err=%v", v, err)
			}
			in := r.input()
			if in.Command != a.Command || in.Task != "查看服务器资源使用情况" {
				t.Fatalf("review did not receive the complete request: %+v", in)
			}
			if in.Where != tc.wantWhere || in.Dir != Redact(tc.dir) || strings.Contains(in.Dir, "fake-context-secret") {
				t.Fatalf("wrong review input context: %+v", in)
			}
			prompt := BuildReviewPrompt(in)
			if !strings.Contains(prompt, "execution location: "+string(tc.wantWhere)+"\n") ||
				!strings.Contains(prompt, "working directory: "+tc.wantDir+"\n") ||
				strings.Contains(prompt, "fake-context-secret") || strings.Contains(prompt, "should-not-be-invented") {
				t.Fatalf("wrong execution context: %s", prompt)
			}
			// The model learns which command the rules did not cover, and
			// that this alone is not danger.
			if !strings.Contains(prompt, "risk: unknown (not covered by the local rule table; not a sign of danger by itself)\n") ||
				!strings.Contains(prompt, "local rule: unlisted command: atop\n") {
				t.Fatalf("unknown risk not explained: %s", prompt)
			}
		})
	}
}

// TestReviewSeesTheTask checks that the reviewer is told what the user
// asked for, redacted and on one line, ahead of the action.
func TestReviewSeesTheTask(t *testing.T) {
	r := &scriptReview{raw: `{"decision":"allow","reason":"asked for"}`}
	g := NewGate(GateConfig{
		Mode:          func() Mode { return ModeReview },
		Scope:         Scope{Home: "/home/user"},
		Prompter:      &scriptPrompt{answers: []Answer{Reject}},
		Reviewer:      r,
		Task:          func() string { return "restart nginx\nplease, password=fake-password-value" },
		ReviewTimeout: time.Second,
		PromptTimeout: time.Second,
	})
	v, err := g.Authorize(context.Background(), cmdAction("systemctl restart nginx"), "")
	if err != nil || !v.Run || v.By != ByReviewer {
		t.Fatalf("%+v %v", v, err)
	}
	r.mu.Lock()
	in := r.last
	r.mu.Unlock()
	if !strings.Contains(in.Task, "restart nginx") || strings.Contains(in.Task, "fake-password-value") {
		t.Fatalf("task %q", in.Task)
	}
	prompt := BuildReviewPrompt(in)
	if !strings.HasPrefix(prompt, "user request: restart nginx please, ") || strings.Index(prompt, "user request") > strings.Index(prompt, "command:") {
		t.Fatalf("prompt %q", prompt)
	}
	if !strings.Contains(ReviewInstructions, `"decision":"allow"|"ask"`) {
		t.Fatal("instructions lack the reply shape")
	}
}
