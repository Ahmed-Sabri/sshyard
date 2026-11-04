package policy

import (
	"strings"
	"unicode"

	"mvdan.cc/sh/v3/syntax"
)

// partial is one command's grade plus flags the walker ORs across a script.
// curl is set when curl or wget runs. interp is set when a shell or a
// scripting interpreter runs, so "curl | sh" can be recognised.
type partial struct {
	cat    Category
	risk   Risk
	prefix string
	why    string
	curl   bool
	interp bool
}

func (p partial) Grade() Grade {
	if p.cat == "" {
		p.cat = "read"
	}
	if p.why == "" {
		p.why = string(p.cat)
	}
	return Grade{Category: p.cat, Risk: p.risk, Prefix: p.prefix, Why: p.why}
}

func unk(why string) partial {
	return partial{cat: "unknown", risk: RiskUnknown, why: why}
}

func crit(prefix, why string) partial {
	return partial{cat: "critical", risk: RiskCritical, prefix: prefix, why: why}
}

func sens(path string) partial {
	return partial{cat: "sensitive-read", risk: RiskMedium, prefix: path, why: "sensitive path"}
}

func highPerm(why string) partial {
	return partial{cat: "permission", risk: RiskHigh, prefix: why, why: why}
}

func catRank(c Category) int {
	switch c {
	case "critical":
		return 12
	case "unknown":
		return 11
	case "permission", "exec-config":
		return 10
	case "network":
		return 9
	case "delete":
		return 8
	case "service":
		return 7
	case "install":
		return 6
	case "write":
		return 5
	case "sensitive-read":
		return 4
	case "low-write":
		return 3
	case "local":
		return 2
	case "local-read":
		return 1
	case "read":
		return 1
	default:
		return 0
	}
}

func better(a, b partial) partial {
	curl := a.curl || b.curl
	interp := a.interp || b.interp
	var w partial
	switch {
	case Rank(b.risk) > Rank(a.risk):
		w = b
	case Rank(a.risk) > Rank(b.risk):
		w = a
	case catRank(b.cat) > catRank(a.cat):
		w = b
	default:
		w = a
	}
	w.curl = curl
	w.interp = interp
	return w
}

func fold(ps []partial) partial {
	if len(ps) == 0 {
		return partial{cat: "read", risk: RiskReadOnly, why: "empty"}
	}
	g := ps[0]
	for _, p := range ps[1:] {
		g = better(g, p)
	}
	return g
}

const maxDepth = 8

type shellEnv struct {
	scope Scope
	dir   string
	local bool
	depth int
}

func (e *shellEnv) clone() *shellEnv {
	c := *e
	return &c
}

func (e *shellEnv) resolve(raw string) (string, bool) {
	if e.local {
		return resolveLocal(raw, e.dir, e.scope.Sandbox)
	}
	return resolveRemote(raw, e.dir, e.scope.Home)
}

func (e *shellEnv) sensitive(raw string) bool {
	if abs, ok := e.resolve(raw); ok {
		return pathSensitive(abs, e.scope)
	}
	return pathSensitive(toSlash(raw), e.scope) || pathSensitive("/"+strings.TrimPrefix(toSlash(raw), "/"), e.scope)
}

func (e *shellEnv) isDisk(raw string) bool {
	if diskGlob(raw) {
		return true
	}
	if abs, ok := e.resolve(raw); ok {
		return diskDevice(abs)
	}
	return diskDevice(raw)
}

func (e *shellEnv) writable(abs string) bool {
	if e.local {
		if e.scope.Sandbox != "" && localWithin(e.scope.Sandbox, abs) {
			return true
		}
		for _, r := range e.scope.WritableRoots {
			if isWinPath(abs) || isWinPath(r) {
				if localWithin(r, abs) {
					return true
				}
				continue
			}
			if withinPOSIX(r, toSlash(abs)) || localWithin(r, abs) {
				return true
			}
		}
		return false
	}
	s := toSlash(abs)
	for _, r := range e.scope.WritableRoots {
		if withinPOSIX(r, s) {
			return true
		}
	}
	return false
}

func (e *shellEnv) criticalPath(raw string) bool {
	if criticalRaw(raw) {
		return true
	}
	abs, ok := e.resolve(raw)
	if !ok {
		return false
	}
	abs = toSlash(abs)
	if abs == "/" {
		return true
	}
	if e.scope.Home != "" && abs == pathCleanHome(e.scope.Home) {
		return true
	}
	if criticalTops[abs] {
		return true
	}
	if strings.HasSuffix(abs, "/*") {
		parent := strings.TrimSuffix(abs, "/*")
		if parent == "" || parent == "/" || criticalTops[parent] || (e.scope.Home != "" && parent == pathCleanHome(e.scope.Home)) {
			return true
		}
	}
	return false
}

func pathCleanHome(h string) string {
	if h == "" {
		return ""
	}
	return strings.TrimRight(toSlash(h), "/")
}

type writeClass int

const (
	writeRedirect writeClass = iota // low inside a writable root
	writeCreate                     // mkdir, touch: low inside a writable root
	writeFileTool                   // write_file: low only under /tmp
	writeMutate                     // mv, cp, sed -i, ln, install: at least medium
)

func (e *shellEnv) gradeWrite(raw string, class writeClass) partial {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" {
		return partial{}
	}
	if harmlessDevice(raw) {
		return partial{}
	}
	if diskGlob(raw) || e.isDisk(raw) {
		return crit("write", "disk device "+raw)
	}
	if strings.ContainsAny(raw, "*?[") {
		return unk("glob write")
	}
	abs, ok := e.resolve(raw)
	if !ok {
		return unk("unresolved write")
	}
	if harmlessDevice(abs) {
		return partial{}
	}
	if diskDevice(abs) {
		return crit("write", "disk device "+abs)
	}
	prefix := dirPrefix(abs)
	if highAuthPath(abs) {
		return partial{cat: "permission", risk: RiskHigh, prefix: prefix, why: "auth path " + abs}
	}
	if pathSensitive(abs, e.scope) {
		return partial{cat: "write", risk: RiskMedium, prefix: prefix, why: "sensitive write " + abs}
	}
	if persistPath(abs) {
		return partial{cat: "write", risk: RiskMedium, prefix: prefix, why: "startup file " + abs}
	}
	if e.writable(abs) && lowWrite(class, abs) {
		return partial{cat: "low-write", risk: RiskLow, prefix: prefix, why: "write " + abs}
	}
	return partial{cat: "write", risk: RiskMedium, prefix: prefix, why: "write " + abs}
}

func lowWrite(class writeClass, abs string) bool {
	switch class {
	case writeRedirect, writeCreate:
		return true
	case writeFileTool:
		return withinPOSIX("/tmp", toSlash(abs))
	default:
		return false
	}
}

func (e *shellEnv) script(src string) partial {
	if e.depth > maxDepth {
		return unk("nested command")
	}
	if forkBomb(src) {
		return crit(":", "fork bomb")
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil || f == nil {
		return unk("parse error")
	}
	child := e.clone()
	child.depth++
	return child.block(f.Stmts)
}

func forkBomb(s string) bool {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return strings.Contains(b.String(), ":(){:|:&};:")
}

func (e *shellEnv) block(stmts []*syntax.Stmt) partial {
	if e.depth > maxDepth {
		return unk("nested command")
	}
	cur := e.clone()
	var gs []partial
	for _, s := range stmts {
		if s == nil {
			continue
		}
		gs = append(gs, cur.stmt(s))
		if d, ok := cur.cdDir(s); ok {
			cur.dir = d
		}
	}
	return fold(gs)
}

func (e *shellEnv) stmt(s *syntax.Stmt) partial {
	if s == nil {
		return partial{}
	}
	g := e.command(s.Cmd)
	r := e.redirs(s.Redirs)
	// A curl/wget redirect is a download into that path. better() would
	// keep "network" when the write is also Medium.
	if g.curl && downloadWrite(r) {
		return mergeDownload(g, r)
	}
	return better(g, r)
}

func downloadWrite(r partial) bool {
	switch r.cat {
	case "write", "low-write", "permission", "critical":
		return true
	default:
		return false
	}
}

func (e *shellEnv) command(cmd syntax.Command) partial {
	switch c := cmd.(type) {
	case nil:
		return partial{}
	case *syntax.CallExpr:
		return e.call(c)
	case *syntax.BinaryCmd:
		return e.binary(c)
	case *syntax.IfClause:
		g := e.block(c.Cond)
		g = better(g, e.block(c.Then))
		if c.Else != nil {
			g = better(g, e.command(c.Else))
		}
		return g
	case *syntax.WhileClause:
		return better(e.block(c.Cond), e.block(c.Do))
	case *syntax.ForClause:
		g := e.block(c.Do)
		if w, ok := c.Loop.(*syntax.WordIter); ok {
			for _, item := range w.Items {
				g = better(g, e.word(item).subst)
			}
		}
		return g
	case *syntax.CaseClause:
		g := e.word(c.Word).subst
		for _, it := range c.Items {
			g = better(g, e.block(it.Stmts))
		}
		return g
	case *syntax.Subshell:
		return e.block(c.Stmts)
	case *syntax.Block:
		return e.block(c.Stmts)
	case *syntax.FuncDecl:
		if c.Body == nil {
			return partial{}
		}
		return e.stmt(c.Body)
	case *syntax.TimeClause:
		if c.Stmt == nil {
			return partial{cat: "read", risk: RiskReadOnly, prefix: "time", why: "time"}
		}
		return raise(e.stmt(c.Stmt), RiskMedium, "")
	case *syntax.CoprocClause:
		if c.Stmt == nil {
			return unk("coproc")
		}
		return e.stmt(c.Stmt)
	case *syntax.DeclClause:
		g := partial{cat: "read", risk: RiskReadOnly, prefix: "declare", why: "shell assignment"}
		for _, a := range c.Args {
			if a != nil && a.Value != nil {
				g = better(g, e.word(a.Value).subst)
			}
		}
		if dangerAssigns(c.Args) {
			g = better(unk("environment"), g)
		}
		return g
	case *syntax.TestClause:
		g := partial{cat: "read", risk: RiskReadOnly, prefix: "[[", why: "test"}
		return better(g, e.nested(c.X))
	case *syntax.ArithmCmd, *syntax.LetClause:
		return better(partial{cat: "read", risk: RiskReadOnly, prefix: "arith", why: "arithmetic"}, e.nested(c))
	default:
		return unk("unsupported syntax")
	}
}

func (e *shellEnv) binary(b *syntax.BinaryCmd) partial {
	if b == nil {
		return partial{}
	}
	left := e.stmt(b.X)
	rightEnv := e
	if b.Op == syntax.AndStmt || b.Op == syntax.OrStmt {
		if d, ok := e.cdDir(b.X); ok {
			rightEnv = e.clone()
			rightEnv.dir = d
		}
	}
	right := rightEnv.stmt(b.Y)
	g := better(left, right)
	if (b.Op == syntax.Pipe || b.Op == syntax.PipeAll) && left.curl && right.interp {
		g = better(g, crit("curl", "pipe to shell"))
	}
	return g
}

func (e *shellEnv) cdDir(s *syntax.Stmt) (string, bool) {
	call, ok := s.Cmd.(*syntax.CallExpr)
	if !ok || call == nil {
		return "", false
	}
	args := e.words(call.Args)
	if len(args) == 0 || !args[0].ok {
		return "", false
	}
	name := baseName(args[0].text)
	if name != "cd" && name != "pushd" {
		return "", false
	}
	if len(args) < 2 {
		if e.scope.Home == "" {
			return "", false
		}
		return e.scope.Home, true
	}
	if !args[1].ok || args[1].text == "-" {
		return "", false
	}
	return e.resolve(args[1].text)
}

func (e *shellEnv) redirs(rs []*syntax.Redirect) partial {
	var gs []partial
	for _, r := range rs {
		gs = append(gs, e.redir(r))
	}
	if len(gs) == 0 {
		return partial{}
	}
	return fold(gs)
}

func (e *shellEnv) redir(r *syntax.Redirect) partial {
	if r == nil {
		return partial{}
	}
	switch r.Op {
	case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return e.nested(r)
	}
	if r.Word == nil {
		return partial{}
	}
	w := e.word(r.Word)
	g := w.subst
	write := true
	switch r.Op {
	case syntax.RdrIn:
		write = false
	case syntax.DplIn:
		if w.ok && isFD(w.text) {
			return g
		}
		write = false
	case syntax.DplOut:
		if w.ok && isFD(w.text) {
			return g
		}
	}
	if !w.ok {
		return better(g, unk("redirect"))
	}
	if write {
		return better(g, e.gradeWrite(w.text, writeRedirect))
	}
	return better(g, sens(w.text))
}

func isFD(s string) bool {
	if s == "-" || s == "" {
		return true
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (e *shellEnv) nested(n syntax.Node) partial {
	if n == nil {
		return partial{}
	}
	var g partial
	syntax.Walk(n, func(node syntax.Node) bool {
		switch s := node.(type) {
		case *syntax.CmdSubst:
			g = better(g, e.block(s.Stmts))
			return false
		case *syntax.ProcSubst:
			inner := e.block(s.Stmts)
			g = better(g, inner)
			if Rank(inner.risk) > Rank(RiskReadOnly) {
				g = better(g, unk("process substitution"))
			}
			return false
		default:
			return true
		}
	})
	return g
}

// arg is one shell word. ok means the text is a literal (quotes removed,
// $HOME / ${HOME} kept as text so path resolution can expand them).
type arg struct {
	text  string
	ok    bool
	subst partial
	proc  bool
	cmd   bool
}

func (e *shellEnv) words(ws []*syntax.Word) []arg {
	out := make([]arg, 0, len(ws))
	for _, w := range ws {
		out = append(out, e.word(w))
	}
	return out
}

func (e *shellEnv) word(w *syntax.Word) arg {
	if w == nil {
		return arg{ok: true}
	}
	var b strings.Builder
	a := arg{ok: true}
	for _, p := range w.Parts {
		e.part(p, &b, &a)
	}
	a.text = b.String()
	return a
}

func (e *shellEnv) part(p syntax.WordPart, b *strings.Builder, a *arg) {
	switch n := p.(type) {
	case *syntax.Lit:
		b.WriteString(n.Value)
	case *syntax.SglQuoted:
		b.WriteString(n.Value)
	case *syntax.DblQuoted:
		for _, c := range n.Parts {
			e.part(c, b, a)
		}
	case *syntax.ParamExp:
		if simpleHome(n) {
			if n.Short {
				b.WriteString("$HOME")
			} else {
				b.WriteString("${HOME}")
			}
			return
		}
		a.ok = false
		if n.Exp != nil && n.Exp.Word != nil {
			inner := e.word(n.Exp.Word)
			a.subst = better(a.subst, inner.subst)
			a.proc = a.proc || inner.proc
			a.cmd = a.cmd || inner.cmd
		}
	case *syntax.CmdSubst:
		a.ok = false
		a.cmd = true
		child := e.clone()
		child.depth++
		a.subst = better(a.subst, child.block(n.Stmts))
	case *syntax.ProcSubst:
		a.ok = false
		a.proc = true
		child := e.clone()
		child.depth++
		inner := child.block(n.Stmts)
		a.subst = better(a.subst, inner)
	case *syntax.ArithmExp:
		a.ok = false
		a.subst = better(a.subst, e.nested(n))
	default:
		a.ok = false
	}
}

func simpleHome(n *syntax.ParamExp) bool {
	return n != nil && n.Param != nil && n.Param.Value == "HOME" &&
		n.Index == nil && n.Slice == nil && n.Repl == nil && n.Exp == nil &&
		!n.Excl && !n.Length && n.NestedParam == nil && len(n.Modifiers) == 0
}

func (e *shellEnv) call(c *syntax.CallExpr) partial {
	if c == nil {
		return partial{}
	}
	args := e.words(c.Args)
	if len(args) == 0 {
		// Bare assignments can change which code a later command runs.
		g := partial{cat: "read", risk: RiskReadOnly, prefix: "assign", why: "assignment"}
		for _, a := range c.Assigns {
			if a != nil && a.Value != nil {
				g = better(g, e.word(a.Value).subst)
			}
		}
		if dangerAssigns(c.Assigns) {
			g = better(unk("environment"), g)
		}
		return g
	}
	g := e.gradeArgs(args)
	if dangerAssigns(c.Assigns) {
		// The command is still graded, so a visible critical stays critical.
		g = better(unk("environment"), g)
	}
	return g
}

func dangerAssigns(as []*syntax.Assign) bool {
	for _, a := range as {
		if a == nil || a.Name == nil {
			continue
		}
		if dangerVar(a.Name.Value) {
			return true
		}
	}
	return false
}

func (e *shellEnv) gradeArgs(args []arg) partial {
	if e.depth > maxDepth {
		return unk("nested command")
	}
	if len(args) == 0 {
		return unk("empty command")
	}
	subst := foldSubsts(args)
	rest, danger := peelLeadAssigns(args)
	if len(rest) == 0 {
		if danger.cat != "" {
			return better(danger, subst)
		}
		return better(unk("empty command"), subst)
	}
	if !rest[0].ok {
		return better(better(unk("command name"), danger), subst)
	}
	name := baseName(rest[0].text)
	if strings.ContainsAny(rest[0].text, "/\\") {
		return better(unk("explicit executable path"), subst)
	}
	var core partial
	if isWrap(name) {
		core = e.wrap(name, rest)
	} else {
		core = e.core(name, rest)
	}
	if core.risk == RiskReadOnly && shellReadsContent(name, rest) {
		core = partial{cat: "sensitive-read", risk: RiskMedium, prefix: core.prefix, why: "unresolved file content read"}
	}
	core = better(core, subst)
	core = dynFloor(core, rest)
	// Unknown ranks above High, so a dangerous assignment is not lowered.
	return better(danger, core)
}

func foldSubsts(args []arg) partial {
	var gs []partial
	for _, a := range args {
		if a.subst.cat != "" || a.subst.risk != 0 || a.subst.curl || a.subst.interp {
			gs = append(gs, a.subst)
		}
	}
	if len(gs) == 0 {
		return partial{}
	}
	return fold(gs)
}

func dynFloor(core partial, args []arg) partial {
	dynamic := false
	proc := false
	bare := false
	substRisk := RiskReadOnly
	for i, a := range args {
		if i > 0 && (!a.ok || a.cmd) {
			dynamic = true
		}
		if i > 0 && !a.ok && !a.cmd && !a.proc {
			bare = true
		}
		if a.proc {
			proc = true
		}
		if Rank(a.subst.risk) > Rank(substRisk) {
			substRisk = a.subst.risk
		}
	}
	// A path reader with a bare $VAR might name a secret. Command
	// substitution is graded on its own (cat $(ls) can stay read-only);
	// its output is not simulated. When unsure, Unknown.
	if bare {
		core = better(core, unk("unresolved path"))
	}
	if dynamic && core.risk != RiskReadOnly {
		core = better(core, unk("command substitution"))
	}
	if proc && !(core.risk == RiskReadOnly && substRisk == RiskReadOnly) {
		core = better(core, unk("process substitution"))
	}
	return core
}

func readsPaths(name string) bool {
	switch name {
	case "ls", "cat", "head", "tail", "less", "more", "grep", "rg", "egrep", "fgrep",
		"stat", "df", "du", "file", "wc", "sort", "uniq", "find":
		return true
	default:
		return false
	}
}

func baseName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func raise(inner partial, floor Risk, elev Category) partial {
	if Rank(inner.risk) < Rank(floor) {
		inner.risk = floor
		if elev != "" && (inner.cat == "read" || inner.cat == "local-read" || inner.cat == "") {
			inner.cat = elev
		}
		if inner.why == "" {
			inner.why = "wrapper"
		} else if !strings.HasPrefix(inner.why, "wrapper") {
			inner.why = "wrapper: " + inner.why
		}
	}
	return inner
}
