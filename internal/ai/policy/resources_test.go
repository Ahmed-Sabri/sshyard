package policy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const resourceQuery = `uname -s; cat /etc/os-release; printf '\n=== Load / CPU ===\n'; uptime; top -b -n 1 | head -n 18; printf '\n=== Memory ===\n'; free -h; printf '\n=== Disk ===\n'; df -hT; printf '\n=== Top memory processes ===\n'; ps -eo pid,comm,%cpu,%mem,rss --sort=-rss | head -n 11`

const resourceQueryWithCPUCount = `printf '=== OS ===\n'; uname -srm; cat /etc/os-release; printf '\n=== Load / CPU ===\n'; uptime; getconf _NPROCESSORS_ONLN; top -b -n 1 | head -n 18; printf '\n=== Memory ===\n'; free -h; printf '\n=== Disk ===\n'; df -hT; printf '\n=== Inodes ===\n'; df -i; printf '\n=== Top memory processes ===\n'; ps -eo pid,comm,%cpu,%mem,rss --sort=-rss | head -n 11`

func TestResourceQueryClassification(t *testing.T) {
	e := NewEngine()
	for _, cmd := range []string{
		"df -hT", "ps -eo pid,comm,%cpu,%mem,rss --sort=-rss",
		"ps -eo pid,comm,%cpu,%mem,rss --sort -rss", "ps -eopid,comm,%cpu,%mem,rss",
		"ps -eo pid,comm,%cpu,%mem,rss", "top -b -n 1 | head -n 18",
		"ps -o time", "ps -e -o pid -o comm", "ps -opid -ocomm",
		"head -n18", "head -qn 18", "tail -c 10", "head -n 18 -",
		"getconf _NPROCESSORS_ONLN", "getconf NPROCESSORS_CONF", "getconf LONG_BIT", "getconf PAGE_SIZE",
		"nproc", "nproc --all", "nproc --ignore=1", "nproc --all --ignore 2", "nproc --help", "nproc --version",
		"FOO=1; getconf _NPROCESSORS_ONLN", "export FOO=1; getconf _NPROCESSORS_ONLN",
	} {
		t.Run(cmd, func(t *testing.T) {
			g := e.Classify(cmdAction(cmd), Scope{Home: "/home/user"})
			if g.Risk != RiskReadOnly || e.Decide(ModeAsk, g, false).Outcome != Allow {
				t.Fatalf("resource query requires approval: %+v", g)
			}
		})
	}
}

func TestResourceQuerySecurityBoundaries(t *testing.T) {
	e := NewEngine()
	for _, tc := range []struct {
		cmd  string
		risk Risk
	}{
		{"ps auxe", RiskMedium}, {"ps eww", RiskMedium}, {"ps auxe -o pid", RiskMedium},
		{"ps -o environ", RiskUnknown}, {"ps -o environ -o pid", RiskUnknown},
		{"ps -oenviron -opid", RiskUnknown}, {"ps -o pid -o environ", RiskUnknown},
		{"ps --sort=environ", RiskUnknown}, {"ps --unknown-option", RiskUnknown},
		{"ps --sort= -rss", RiskUnknown}, {"ps --sort=+-rss", RiskUnknown},
		{"ps -o", RiskUnknown}, {"ps --sort", RiskUnknown}, {"ps -o '$FIELDS'", RiskUnknown},
		{"ps -o $FIELDS", RiskUnknown}, {"ps -o pid --sort=$(cat /etc/shadow)", RiskUnknown},
		{"head -n 18 /etc/shadow", RiskMedium}, {"head -n18 /tmp/report", RiskMedium},
		{"tail -c10 ~/.ssh/id_rsa", RiskMedium}, {"head -n18 < /etc/shadow", RiskMedium},
		{"head -- /etc/shadow", RiskMedium}, {"head -n18 -- /tmp/*", RiskMedium},
		{"head -n18 -- -secret", RiskMedium}, {"head -n18 $FILE", RiskUnknown},
		{"head --unknown-option", RiskUnknown}, {"df -hT /etc/shadow", RiskMedium},
		{"du -T", RiskUnknown}, {"df --unknown-option", RiskUnknown},
		{"cat /etc/os-release", RiskMedium}, {"rm -rf /", RiskCritical},
		{"chmod 777 /etc/shadow", RiskHigh}, {"unverified-program", RiskUnknown},
		{"getconf", RiskUnknown}, {"getconf -a", RiskUnknown}, {"getconf --unknown", RiskUnknown},
		{"getconf -v POSIX_V7_LP64_OFF64 _NPROCESSORS_ONLN", RiskUnknown},
		{"getconf -vPOSIX_V7_LP64_OFF64 _NPROCESSORS_ONLN", RiskUnknown},
		{"getconf _NPROCESSORS_ONLN /tmp", RiskUnknown}, {"getconf NAME_MAX /etc/shadow", RiskUnknown},
		{"getconf '_NPROCESSORS_ONLN /tmp'", RiskUnknown}, {"getconf /tmp/custom", RiskUnknown},
		{"getconf $VARIABLE", RiskUnknown}, {"getconf $(echo _NPROCESSORS_ONLN)", RiskUnknown},
		{"/tmp/getconf _NPROCESSORS_ONLN", RiskUnknown}, {"GETCONF_DIR=/tmp getconf _NPROCESSORS_ONLN", RiskUnknown},
		{"env GETCONF_DIR=/tmp getconf _NPROCESSORS_ONLN", RiskUnknown},
		{"export GETCONF_DIR=/tmp; getconf _NPROCESSORS_ONLN", RiskUnknown},
		{"GETCONF_DIR=/tmp; export GETCONF_DIR; getconf _NPROCESSORS_ONLN", RiskUnknown},
		{"export PATH=/tmp; nproc", RiskUnknown}, {"PATH=/tmp; nproc", RiskUnknown},
		{"export LD_PRELOAD=/tmp/library; nproc", RiskUnknown}, {"LD_PRELOAD=/tmp/library; nproc", RiskUnknown},
		{"export GETCONF_DIR=/tmp; rm -rf /", RiskCritical},
		{"GETCONF_DIR=/tmp; rm -rf /", RiskCritical},
		{"nproc --ignore", RiskUnknown}, {"nproc --ignore=", RiskUnknown}, {"nproc --ignore=-1", RiskUnknown},
		{"nproc --ignore=all", RiskUnknown}, {"nproc --ignore=1 --ignore=bad", RiskUnknown},
		{"nproc --unknown", RiskUnknown}, {"nproc --all=1", RiskUnknown}, {"nproc /tmp/custom", RiskUnknown},
		{"nproc --ignore=$COUNT", RiskUnknown}, {"nproc --ignore=$(echo 1)", RiskUnknown},
		{"/tmp/nproc", RiskUnknown},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			g := e.Classify(cmdAction(tc.cmd), Scope{Home: "/home/user"})
			if g.Risk != tc.risk {
				t.Fatalf("grade=%+v want risk=%s", g, tc.risk)
			}
			if d := e.Decide(ModeReview, g, true); d.Outcome == Allow {
				t.Fatalf("unsafe command auto-allowed: %+v", d)
			}
		})
	}
}

func TestResourceQueryCallsReviewer(t *testing.T) {
	r := &scriptReview{raw: `{"decision":"allow","reason":"requested resource inspection"}`}
	p := &scriptPrompt{}
	g := NewGate(GateConfig{
		Mode: func() Mode { return ModeReview }, Scope: Scope{},
		Reviewer: r, Prompter: p, Task: func() string { return "查看服务器资源使用情况" },
	})
	v, err := g.Authorize(context.Background(), cmdAction(resourceQuery), "")
	if err != nil || !v.Run || v.By != ByReviewer || v.Risk != RiskMedium || r.n() != 1 || len(p.seen()) != 0 {
		t.Fatalf("verdict=%+v err=%v reviews=%d prompts=%d", v, err, r.n(), len(p.seen()))
	}
	if in := r.input(); in.Command != resourceQuery || in.Task != "查看服务器资源使用情况" {
		t.Fatalf("review did not receive the requested action: %+v", in)
	}
	safeQuery := strings.Replace(resourceQuery, "cat /etc/os-release; ", "", 1)
	v, err = g.Authorize(context.Background(), cmdAction(safeQuery), "")
	if err != nil || !v.Run || v.By != ByReviewer || v.Risk != RiskReadOnly || r.n() != 2 || len(p.seen()) != 0 {
		t.Fatalf("safe resource query verdict=%+v err=%v reviews=%d prompts=%d", v, err, r.n(), len(p.seen()))
	}
}

func TestResourceQueryWithCPUCountCallsReviewer(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		err         error
		run         bool
		prompts     int
	}{
		{"allow", `{"decision":"allow","reason":"requested resource inspection"}`, nil, true, 0},
		{"ask", `{"decision":"ask","reason":"needs confirmation"}`, nil, false, 1},
		{"failure", "", errors.New("review unavailable"), false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &scriptReview{raw: tc.reply, err: tc.err}
			p := &scriptPrompt{}
			g := NewGate(GateConfig{
				Mode: func() Mode { return ModeReview }, Scope: Scope{},
				Reviewer: r, Prompter: p, Task: func() string { return "查看服务器资源使用情况" },
			})
			a := cmdAction(resourceQueryWithCPUCount)
			a.Dir = "/"
			v, err := g.Authorize(context.Background(), a, "")
			if err != nil || v.Run != tc.run || v.Risk != RiskMedium || r.n() != 1 || len(p.seen()) != tc.prompts {
				t.Fatalf("verdict=%+v err=%v reviews=%d prompts=%d", v, err, r.n(), len(p.seen()))
			}
			if tc.run && v.By != ByReviewer || !tc.run && v.ReviewReason == "" {
				t.Fatalf("missing review outcome: %+v", v)
			}
			if tc.run {
				a.Command = strings.Replace(a.Command, "cat /etc/os-release; ", "", 1)
				v, err = g.Authorize(context.Background(), a, "")
				if err != nil || !v.Run || v.By != ByReviewer || v.Risk != RiskReadOnly || r.n() != 2 || len(p.seen()) != 0 {
					t.Fatalf("safe resource query verdict=%+v err=%v reviews=%d prompts=%d", v, err, r.n(), len(p.seen()))
				}
			}
		})
	}
}

func TestResourceQueryEnvironmentAssignmentsRequireConfirmation(t *testing.T) {
	for _, command := range []string{
		"export GETCONF_DIR=/tmp; getconf _NPROCESSORS_ONLN",
		"GETCONF_DIR=/tmp; export GETCONF_DIR; getconf _NPROCESSORS_ONLN",
	} {
		t.Run(command, func(t *testing.T) {
			r := &scriptReview{raw: `{"decision":"ask","reason":"needs confirmation"}`}
			p := &scriptPrompt{}
			g := testGate(ModeReview, p, r, nil)
			v, err := g.Authorize(context.Background(), cmdAction(command), "")
			if err != nil || v.Run || v.Risk != RiskUnknown || r.n() != 1 || len(p.seen()) != 1 {
				t.Fatalf("verdict=%+v err=%v reviews=%d prompts=%d", v, err, r.n(), len(p.seen()))
			}
		})
	}
}
