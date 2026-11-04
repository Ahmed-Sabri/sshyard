package policy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConfiguredReviewRoutesEveryRisk(t *testing.T) {
	for _, action := range []struct {
		command string
		risk    Risk
	}{
		{"uname -srm", RiskReadOnly}, {"mkdir /tmp/resources", RiskLow},
		{"cat /etc/os-release", RiskMedium}, {"chmod 644 /tmp/resources", RiskHigh},
		{"rm -rf /", RiskCritical},
		{resourceQueryWithCPUCount + "; vmstat 1 3", RiskUnknown},
	} {
		for _, review := range []struct {
			name, reply string
			err         error
			block, run  bool
		}{
			{"allow", `{"decision":"allow","reason":"covered by request"}`, nil, false, true},
			{"ask", `{"decision":"ask","reason":"needs confirmation"}`, nil, false, false},
			{"error", "", errors.New("unavailable"), false, false},
			{"timeout", "", nil, true, false},
		} {
			t.Run(action.risk.String()+"/"+review.name, func(t *testing.T) {
				r := &scriptReview{raw: review.reply, err: review.err, block: review.block}
				p, aud := &scriptPrompt{}, &memAudit{}
				g := NewGate(GateConfig{
					Mode: func() Mode { return ModeReview }, Scope: Scope{},
					Reviewer: r, Prompter: p, Auditor: aud, ReviewTimeout: 10 * time.Millisecond,
					Task: func() string { return "查看服务器资源使用情况" },
				})
				a := cmdAction(action.command)
				a.Dir = "/"
				v, err := g.Authorize(context.Background(), a, "")
				if err != nil || v.Run != review.run || v.Risk != action.risk || r.n() != 1 {
					t.Fatalf("verdict=%+v err=%v reviews=%d prompts=%d", v, err, r.n(), len(p.seen()))
				}
				if review.run {
					if v.By != ByReviewer || len(p.seen()) != 0 {
						t.Fatalf("reviewer allow was not honored: %+v prompts=%d", v, len(p.seen()))
					}
					g.Done(a, v, 0, 1, 0)
					if records := aud.list(); len(records) != 1 || records[0].Decider != "reviewer" || records[0].Verdict != "reviewer-allow" {
						t.Fatalf("missing reviewer audit source: %+v", records)
					}
				} else if len(p.seen()) != 1 || v.ReviewReason == "" {
					t.Fatalf("review did not ask the user: %+v prompts=%d", v, len(p.seen()))
				}
				if in := r.input(); in.Command != a.Command || in.Risk != action.risk || in.Task != "查看服务器资源使用情况" {
					t.Fatalf("reviewer received the wrong action: %+v", in)
				}
			})
		}
	}
}

func TestConfiguredReviewAskCannotReuseManualGrant(t *testing.T) {
	mode := ModeAsk
	r := &scriptReview{raw: `{"decision":"ask","reason":"needs confirmation"}`}
	p := &scriptPrompt{answers: []Answer{AllowSimilar, Reject}}
	g := NewGate(GateConfig{Mode: func() Mode { return mode }, Reviewer: r, Prompter: p})
	a := cmdAction("systemctl restart nginx")
	first, err := g.Authorize(context.Background(), a, "")
	if err != nil || !first.Run || first.Answer != AllowSimilar {
		t.Fatalf("failed to set up manual grant: %+v err=%v", first, err)
	}
	mode = ModeReview
	v, err := g.Authorize(context.Background(), a, "")
	if err != nil || v.Run || r.n() != 1 || len(p.seen()) != 2 || v.Similar {
		t.Fatalf("reviewer ask bypassed by manual grant: %+v err=%v reviews=%d prompts=%d", v, err, r.n(), len(p.seen()))
	}
}

func TestConfiguredReviewRechecksSameCommandForNewTask(t *testing.T) {
	task := "restart nginx"
	r := &scriptReview{raw: `{"decision":"allow","reason":"requested"}`}
	p := &scriptPrompt{}
	g := NewGate(GateConfig{
		Mode: func() Mode { return ModeReview }, Task: func() string { return task },
		Reviewer: r, Prompter: p,
	})
	a := cmdAction("systemctl restart nginx")
	first, err := g.Authorize(context.Background(), a, "")
	if err != nil || !first.Run || first.By != ByReviewer {
		t.Fatalf("first review failed: %+v err=%v", first, err)
	}
	task = "only inspect nginx"
	r.raw = `{"decision":"ask","reason":"restart no longer requested"}`
	v, err := g.Authorize(context.Background(), a, "")
	if err != nil || v.Run || r.n() != 2 || len(p.seen()) != 1 || r.input().Task != task {
		t.Fatalf("reused an old review: %+v err=%v reviews=%d prompts=%d", v, err, r.n(), len(p.seen()))
	}
}

func TestOtherPermissionRoutesRemainUnchanged(t *testing.T) {
	e := NewEngine()
	for _, tc := range []struct {
		risk                    Risk
		ask, full, unconfigured Outcome
	}{
		{RiskReadOnly, Allow, Allow, Allow}, {RiskLow, Ask, Allow, Allow},
		{RiskMedium, Ask, Allow, Ask}, {RiskHigh, Ask, Allow, Ask},
		{RiskCritical, Ask, Allow, Ask}, {RiskUnknown, Ask, Ask, Ask},
	} {
		t.Run(tc.risk.String(), func(t *testing.T) {
			grade := Grade{Risk: tc.risk}
			for _, configured := range []bool{false, true} {
				if d := e.Decide(ModeAsk, grade, configured); d.Outcome != tc.ask {
					t.Fatalf("ask route changed: %+v", d)
				}
				if d := e.Decide(ModeFull, grade, configured); d.Outcome != tc.full {
					t.Fatalf("full route changed: %+v", d)
				}
			}
			if d := e.Decide(ModeReview, grade, false); d.Outcome != tc.unconfigured {
				t.Fatalf("unconfigured review route changed: %+v", d)
			}
		})
	}
}

func TestConfiguredReviewHonorsManualAllowOnce(t *testing.T) {
	r := &scriptReview{raw: `{"decision":"ask","reason":"needs confirmation"}`}
	p := &scriptPrompt{answers: []Answer{AllowOnce, Reject}}
	g := testGate(ModeReview, p, r, nil)
	a := cmdAction("vmstat 1 3")
	v, err := g.Authorize(context.Background(), a, "")
	if err != nil || !v.Run || v.By != ByUser || v.Answer != AllowOnce || v.Similar || r.n() != 1 || len(p.seen()) != 1 {
		t.Fatalf("manual allow once was not honored: %+v err=%v", v, err)
	}
	v, err = g.Authorize(context.Background(), a, "")
	if err != nil || v.Run || r.n() != 2 || len(p.seen()) != 2 {
		t.Fatalf("manual allow once authorized another attempt: %+v err=%v", v, err)
	}
}
