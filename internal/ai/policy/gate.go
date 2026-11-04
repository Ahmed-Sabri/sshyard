package policy

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultPromptTimeout is how long the user has to answer. Silence is a reject.
	DefaultPromptTimeout = 5 * time.Minute
	// DefaultReviewTimeout is how long the review model has. Failure asks the user.
	DefaultReviewTimeout = 10 * time.Second

	refusalReject  = "rejected by the user"
	refusalTimeout = "confirmation timed out; treated as rejected"
	refusalCancel  = "cancelled"
	refusalAudit   = "audit log is unavailable"
	refusalNoAsk   = "confirmation is unavailable; treated as rejected"
)

// GateConfig builds one gate for one AI pane session.
// A nil Engine uses NewEngine. A nil Mode reports ModeAsk.
// A nil Reviewer means review mode asks the user for Medium actions.
// A nil Auditor skips the log. PromptTimeout and ReviewTimeout of 0
// use the defaults (5 minutes and 10 seconds).
// Output, when set, supplies the recent untrusted terminal text the
// reviewer may see (the gate keeps the last 20 lines). Task, when set,
// supplies what the user asked for in the current turn.
type GateConfig struct {
	Engine        Engine
	Mode          func() Mode
	Scope         Scope
	Prompter      Prompter
	Reviewer      Reviewer
	Auditor       Auditor
	SessionID     string
	PaneID        string
	Agent         string
	Host          string
	HostMeta      string
	Output        func() string
	Task          func() string
	Now           func() time.Time
	PromptTimeout time.Duration
	ReviewTimeout time.Duration
}

type gate struct {
	eng           Engine
	mode          func() Mode
	scope         Scope
	prompter      Prompter
	reviewer      Reviewer
	auditor       Auditor
	session       string
	pane          string
	agent         string
	host          string
	hostMeta      string
	output        func() string
	task          func() string
	nowFn         func() time.Time
	promptTimeout time.Duration
	reviewTimeout time.Duration

	mu      sync.Mutex
	grants  []grant
	pending []pendingRec
	// reviews are the reviewer's answers in this session, by action, so
	// the same action is not reviewed twice.
	reviews map[string]reviewMemo
}

type reviewMemo struct {
	allow  bool
	reason string
}

// maxReviewMemos bounds the answers a session remembers.
const maxReviewMemos = 256

type grant struct {
	cat    Category
	prefix string
}

type pendingRec struct {
	key string
	rec AuditRecord
}

// NewGate returns the session gate described by cfg.
func NewGate(cfg GateConfig) Gate {
	if cfg.Engine == nil {
		cfg.Engine = NewEngine()
	}
	if cfg.Mode == nil {
		cfg.Mode = func() Mode { return ModeAsk }
	}
	if cfg.PromptTimeout <= 0 {
		cfg.PromptTimeout = DefaultPromptTimeout
	}
	if cfg.ReviewTimeout <= 0 {
		cfg.ReviewTimeout = DefaultReviewTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HostMeta == "" {
		cfg.HostMeta = cfg.Host
	}
	return &gate{
		eng:           cfg.Engine,
		mode:          cfg.Mode,
		scope:         cfg.Scope,
		prompter:      cfg.Prompter,
		reviewer:      cfg.Reviewer,
		task:          cfg.Task,
		auditor:       cfg.Auditor,
		session:       cfg.SessionID,
		pane:          cfg.PaneID,
		agent:         cfg.Agent,
		host:          cfg.Host,
		hostMeta:      cfg.HostMeta,
		output:        cfg.Output,
		nowFn:         cfg.Now,
		promptTimeout: cfg.PromptTimeout,
		reviewTimeout: cfg.ReviewTimeout,
	}
}

func (g *gate) Authorize(ctx context.Context, a Action, diff string) (Verdict, error) {
	mode := g.mode()
	if !mode.Valid() {
		mode = ModeAsk
	}
	d := g.eng.Decide(mode, g.eng.Classify(a, g.scope), g.reviewer != nil)
	v := Verdict{Decision: d}

	if ctx.Err() != nil {
		return g.stop(v, a, diff, Cancelled, refusalCancel)
	}

	if d.Outcome == Review && g.reviewer != nil {
		allow, reason, err := g.callReview(ctx, g.reviewInput(a, d, diff))
		if ctx.Err() != nil {
			return g.stop(v, a, diff, Cancelled, refusalCancel)
		}
		if err != nil {
			if reason == "" {
				if errors.Is(err, context.DeadlineExceeded) {
					reason = "review timed out"
				} else {
					reason = "review failed"
				}
			}
			v.ReviewReason = reason
			d.Outcome = Ask
			v.Decision = d
		} else if allow {
			v.Run = true
			v.By = ByReviewer
			v.Outcome = Allow
			v.ReviewReason = reason
			return g.commit(v, a, diff)
		} else {
			v.ReviewReason = reason
			d.Outcome = Ask
			v.Decision = d
		}
	}

	if v.Outcome == Allow {
		v.Run = true
		return g.commit(v, a, diff)
	}

	if g.hasGrant(v, a) {
		v.Run = true
		v.By = ByUser
		v.Answer = AllowSimilar
		v.Outcome = Allow
		return g.commit(v, a, diff)
	}
	if g.prompter == nil {
		return g.stop(v, a, diff, Reject, refusalNoAsk)
	}
	switch g.callPrompt(ctx, Prompt{Action: a, Decision: v.Decision, Diff: diff, ReviewNote: v.ReviewReason}) {
	case AllowOnce:
		v.Run = true
		v.By = ByUser
		v.Answer = AllowOnce
		v.Outcome = Allow
	case AllowSimilar:
		v.Run = true
		v.By = ByUser
		v.Outcome = Allow
		if v.Similar && (v.Risk == RiskLow || v.Risk == RiskMedium) {
			v.Answer = AllowSimilar
			g.addGrant(v)
		} else {
			v.Answer = AllowOnce
		}
	case TimedOut:
		return g.stop(v, a, diff, TimedOut, refusalTimeout)
	case Cancelled:
		return g.stop(v, a, diff, Cancelled, refusalCancel)
	default:
		return g.stop(v, a, diff, Reject, refusalReject)
	}
	return g.commit(v, a, diff)
}

func (g *gate) stop(v Verdict, a Action, diff string, ans Answer, refusal string) (Verdict, error) {
	v.Run = false
	v.Answer = ans
	v.Refusal = refusal
	return g.commit(v, a, diff)
}

func (g *gate) Done(a Action, v Verdict, exit int, durMS int64, outBytes int64) {
	rec, ok := g.pop(a)
	if !ok {
		rec = g.build(v, a, "")
	}
	code := exit
	rec.ExitCode = &code
	rec.DurationMS = durMS
	rec.OutputBytes = outBytes
	// exit < 0 is the cancel/timeout sentinel; the allow-time verdict would hide it.
	if exit < 0 {
		rec.Verdict = "cancelled"
	}
	_ = g.write(rec)
}

// callReview asks the reviewer about in, or answers as it did for the same
// action earlier in the session. Only answers are remembered: a failure is
// asked again next time.
func (g *gate) callReview(ctx context.Context, in ReviewInput) (bool, string, error) {
	key := reviewKey(in)
	g.mu.Lock()
	memo, seen := g.reviews[key]
	g.mu.Unlock()
	if seen {
		return memo.allow, memo.reason, nil
	}
	cctx, cancel := context.WithTimeout(ctx, g.reviewTimeout)
	defer cancel()
	allow, reason, err := g.reviewer.Review(cctx, in)
	if ctx.Err() != nil {
		return false, "", ctx.Err()
	}
	// A late reply is not a decision, and its text is not a reason.
	if cctx.Err() != nil {
		return false, "", cctx.Err()
	}
	if err == nil {
		g.mu.Lock()
		if g.reviews == nil || len(g.reviews) >= maxReviewMemos {
			g.reviews = map[string]reviewMemo{}
		}
		g.reviews[key] = reviewMemo{allow, reason}
		g.mu.Unlock()
	}
	return allow, reason, err
}

// reviewKey is the action a review judged: what it does and where, not
// the terminal around it or the turn that asked for it.
func reviewKey(in ReviewInput) string {
	return strings.Join([]string{string(in.Category), in.Risk.String(), in.Command, in.Path, in.DiffSum}, "\x00")
}

func (g *gate) callPrompt(ctx context.Context, p Prompt) Answer {
	cctx, cancel := context.WithTimeout(ctx, g.promptTimeout)
	defer cancel()
	ans := g.prompter.Ask(cctx, p)
	if ctx.Err() != nil {
		return Cancelled
	}
	if cctx.Err() != nil {
		return TimedOut
	}
	return ans
}

func (g *gate) reviewInput(a Action, d Decision, diff string) ReviewInput {
	out := ""
	if g.output != nil {
		out = lastLines(g.output(), 20)
	}
	task := ""
	if g.task != nil {
		task = g.task()
	}
	return ReviewInput{
		Task:     Redact(truncRunes(task, maxReviewTask)),
		Category: d.Category,
		Risk:     d.Risk,
		Command:  Redact(a.Command),
		Path:     Redact(a.Path),
		DiffSum:  diffSummary(diff),
		HostMeta: Redact(g.hostMeta),
		Reason:   Redact(a.Reason),
		Output:   Redact(out),
	}
}

func (g *gate) commit(v Verdict, a Action, diff string) (Verdict, error) {
	if v.Run {
		if err := g.prepare(); err != nil {
			v.Run = false
			v.Refusal = refusalAudit
			return v, err
		}
		g.push(a, g.build(v, a, diff))
		return v, nil
	}
	if err := g.write(g.build(v, a, diff)); err != nil {
		v.Refusal = refusalAudit
		return v, err
	}
	return v, nil
}

func (g *gate) prepare() error {
	if g.auditor == nil {
		return nil
	}
	if p, ok := g.auditor.(AuditPreparer); ok {
		return p.Prepare()
	}
	return nil
}

func (g *gate) write(rec AuditRecord) error {
	if g.auditor == nil {
		return nil
	}
	return g.auditor.Record(rec)
}

func (g *gate) build(v Verdict, a Action, diff string) AuditRecord {
	verdict, decider := verdictOf(v)
	hash, lines := diffStats(diff)
	cmd := Redact(a.Command)
	if strings.TrimSpace(a.Command) == "" {
		cmd = Redact(a.Path)
	}
	return AuditRecord{
		Time:         g.nowFn(),
		SessionID:    g.session,
		PaneID:       g.pane,
		Agent:        g.agent,
		Host:         g.host,
		Mode:         string(v.Mode),
		Where:        string(a.Where),
		Category:     string(v.Category),
		Risk:         v.Risk.String(),
		Command:      cmd,
		DiffHash:     hash,
		DiffLines:    lines,
		Verdict:      verdict,
		Decider:      decider,
		ReviewReason: Redact(v.ReviewReason),
	}
}

func (g *gate) hasGrant(v Verdict, a Action) bool {
	if !v.Similar || v.Risk == RiskHigh || v.Risk == RiskCritical || v.Risk == RiskUnknown {
		return false
	}
	if v.Risk != RiskLow && v.Risk != RiskMedium {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, gr := range g.grants {
		if gr.cat == v.Category && grantCovers(gr.prefix, v.Prefix, a.Path) {
			return true
		}
	}
	return false
}

func (g *gate) addGrant(v Verdict) {
	if v.Prefix == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grants = append(g.grants, grant{cat: v.Category, prefix: v.Prefix})
}

func grantCovers(grantPrefix, gradePrefix, path string) bool {
	if grantPrefix == "" {
		return false
	}
	if grantPrefix == gradePrefix {
		return true
	}
	return underDir(grantPrefix, path) || underDir(grantPrefix, gradePrefix)
}

func underDir(root, target string) bool {
	root = strings.TrimRight(toSlash(root), "/")
	target = toSlash(target)
	if root == "" || target == "" || root == target {
		return false
	}
	return strings.HasPrefix(target, root+"/")
}

func actionKey(a Action) string {
	return string(a.Where) + "\x00" + a.Tool + "\x00" + a.Command + "\x00" + a.Path
}

func (g *gate) push(a Action, rec AuditRecord) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pending = append(g.pending, pendingRec{key: actionKey(a), rec: rec})
}

func (g *gate) pop(a Action) (AuditRecord, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := actionKey(a)
	for i, p := range g.pending {
		if p.key != key {
			continue
		}
		g.pending = append(g.pending[:i], g.pending[i+1:]...)
		return p.rec, true
	}
	return AuditRecord{}, false
}
