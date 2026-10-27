package policy

import (
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// Remote paths are POSIX. Local paths use filepath, and also accept
// Windows drive and UNC forms when the agent runs on Windows (the
// helper understands those forms on every GOOS so tests can feed both).

func normalizeScope(s Scope) Scope {
	if s.Home != "" {
		if h, ok := resolveRemote(s.Home, "", ""); ok {
			s.Home = h
		}
	}
	if len(s.WritableRoots) == 0 {
		if s.Home != "" {
			s.WritableRoots = []string{s.Home, "/tmp"}
		} else {
			s.WritableRoots = []string{"/tmp"}
		}
		return s
	}
	roots := make([]string, 0, len(s.WritableRoots))
	for _, r := range s.WritableRoots {
		if abs, ok := resolveRemote(r, s.Home, s.Home); ok {
			roots = append(roots, abs)
		}
	}
	s.WritableRoots = roots
	return s
}

// resolveRemote expands ~ and $HOME / ${HOME} to home, joins a relative
// path onto dir (or home), and cleans with path.Clean. It does not touch
// the filesystem. ~user and other expansions return ok=false.
func resolveRemote(raw, dir, home string) (string, bool) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", false
	}
	switch {
	case p == "~" || p == "~/":
		if home == "" {
			return "", false
		}
		p = home
	case strings.HasPrefix(p, "~/"):
		if home == "" {
			return "", false
		}
		p = path.Join(home, p[2:])
	case p == "$HOME" || p == "${HOME}" || p == "$HOME/" || p == "${HOME}/":
		if home == "" {
			return "", false
		}
		p = home
	case strings.HasPrefix(p, "$HOME/"):
		if home == "" {
			return "", false
		}
		p = path.Join(home, strings.TrimPrefix(p, "$HOME/"))
	case strings.HasPrefix(p, "${HOME}/"):
		if home == "" {
			return "", false
		}
		p = path.Join(home, strings.TrimPrefix(p, "${HOME}/"))
	case strings.HasPrefix(p, "~") || strings.Contains(p, "$"):
		// ~other or an unknown variable. A glob such as /* has no '$'.
		if strings.Contains(p, "$") || (strings.HasPrefix(p, "~") && !strings.HasPrefix(p, "~/")) {
			return "", false
		}
	}
	if !strings.HasPrefix(p, "/") {
		base := dir
		if base == "" {
			base = home
		}
		if base == "" {
			return "", false
		}
		if !strings.HasPrefix(base, "/") && home != "" {
			base = path.Join(home, base)
		}
		p = path.Join(base, p)
	}
	return path.Clean(p), true
}

func resolveLocal(raw, dir, sandbox string) (string, bool) {
	p := strings.TrimSpace(raw)
	if p == "" || p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return "", false
	}
	if strings.Contains(p, "$") {
		return "", false
	}
	if isWinPath(p) || ((p == "" || !filepath.IsAbs(p)) && (isWinPath(dir) || (dir == "" && isWinPath(sandbox)))) {
		if !isWinPath(p) {
			base := dir
			if base == "" {
				base = sandbox
			}
			if base == "" {
				return "", false
			}
			p = strings.TrimRight(base, `/\`) + `\` + p
		}
		return cleanWin(p), true
	}
	if !filepath.IsAbs(p) {
		base := dir
		if base == "" {
			base = sandbox
		}
		if base == "" {
			return "", false
		}
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p), true
}

func isWinPath(p string) bool {
	if len(p) >= 3 && isLetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	if len(p) >= 2 && p[0] == p[1] && (p[0] == '\\' || p[0] == '/') {
		return true
	}
	return false
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func cleanWin(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	vol := ""
	unc := false
	rest := p
	switch {
	case strings.HasPrefix(p, `\\`):
		unc = true
		rest = strings.TrimLeft(p, `\`)
	case len(p) >= 2 && isLetter(p[0]) && p[1] == ':':
		vol = p[:2]
		rest = p[2:]
	}
	var stack []string
	for _, part := range strings.Split(rest, `\`) {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			stack = append(stack, part)
		}
	}
	joined := strings.Join(stack, `\`)
	switch {
	case unc:
		if joined == "" {
			return `\\`
		}
		return `\\` + joined
	case vol != "":
		if joined == "" {
			return vol + `\`
		}
		return vol + `\` + joined
	case joined == "":
		return "."
	default:
		return joined
	}
}

func toSlash(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

func withinPOSIX(root, target string) bool {
	root = path.Clean(root)
	target = path.Clean(target)
	if root == "/" {
		return strings.HasPrefix(target, "/")
	}
	return target == root || strings.HasPrefix(target, root+"/")
}

func localWithin(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	if isWinPath(root) || isWinPath(target) {
		if !isWinPath(root) || !isWinPath(target) {
			return false
		}
		return winWithin(root, target)
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func winWithin(root, target string) bool {
	r := strings.TrimRight(cleanWin(root), `\`)
	t := cleanWin(target)
	if r == "" || t == "" {
		return false
	}
	if strings.EqualFold(t, r) {
		return true
	}
	if len(t) <= len(r) || t[len(r)] != '\\' {
		return false
	}
	return strings.EqualFold(t[:len(r)], r)
}

// sensitivePatterns are the built-in secret paths plus Scope.Sensitive.
// Globs use path.Match. "**" matches across directories. A pattern with
// no slash matches the last path segment (so "*.pem" matches anywhere).
func sensitivePatterns(scope Scope) []string {
	base := []string{
		"/etc/shadow",
		"/etc/gshadow",
		"/etc/sudoers",
		"/etc/sudoers*",
		"/etc/sudoers.d/**",
		"**/.ssh/**",
		"**/*.pem",
		"**/*.key",
		"**/id_*",
		"**/.env",
		"**/.env.*",
		"**/.netrc",
		"**/.pgpass",
		"**/.aws/credentials",
		"**/.kube/config",
		"**/kubeconfig",
		"**/authorized_keys",
		"/proc/*/environ",
		".*_history",
		"**/.docker/config.json",
		"**/.git-credentials",
		"**/.config/gh/hosts.yml",
		"/etc/ssl/private/**",
	}
	for _, p := range scope.Sensitive {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		base = append(base, expandTildePattern(p, scope.Home))
	}
	return base
}

func expandTildePattern(p, home string) string {
	if home == "" {
		return p
	}
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return path.Join(home, p[2:])
	default:
		return p
	}
}

func pathSensitive(abs string, scope Scope) bool {
	if abs == "" {
		return false
	}
	abs = toSlash(abs)
	pats := sensitivePatterns(scope)
	cur := abs
	for {
		for _, pat := range pats {
			if globMatch(pat, cur) {
				return true
			}
		}
		parent := path.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return false
}

func globMatch(pattern, candidate string) bool {
	pattern = strings.TrimSpace(pattern)
	candidate = toSlash(candidate)
	if pattern == "" || candidate == "" {
		return false
	}
	if !strings.Contains(pattern, "/") {
		ok, err := path.Match(pattern, path.Base(candidate))
		return err == nil && ok
	}
	return matchSegments(splitSeg(pattern), splitSeg(candidate))
}

func splitSeg(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func matchSegments(pat, name []string) bool {
	var rec func(pi, ni int) bool
	rec = func(pi, ni int) bool {
		for pi < len(pat) {
			if pat[pi] == "**" {
				if rec(pi+1, ni) {
					return true
				}
				if ni < len(name) && rec(pi, ni+1) {
					return true
				}
				return false
			}
			if ni >= len(name) {
				return false
			}
			ok, err := path.Match(pat[pi], name[ni])
			if err != nil || !ok {
				return false
			}
			pi++
			ni++
		}
		return ni == len(name)
	}
	return rec(0, 0)
}

// highAuthPath is a write that changes authentication or a credential
// store: shadow, sudoers, authorized_keys, and anything under a .ssh
// directory. Other sensitive writes stay Medium.
func highAuthPath(abs string) bool {
	s := toSlash(abs)
	switch s {
	case "/etc/shadow", "/etc/gshadow", "/etc/sudoers":
		return true
	}
	if strings.HasPrefix(s, "/etc/sudoers") {
		return true
	}
	if path.Base(s) == "authorized_keys" {
		return true
	}
	if strings.Contains(s, "/.ssh/") || strings.HasSuffix(s, "/.ssh") {
		return true
	}
	return false
}

// persistPath is a file that runs later: shell startup, systemd, cron,
// package-manager config, or an autostart entry. A write is at least Medium
// even when the path sits inside a writable root.
func persistPath(abs string) bool {
	s := toSlash(abs)
	switch path.Base(s) {
	case ".bashrc", ".bash_profile", ".profile", ".zshrc", ".zprofile", ".bash_login",
		".gitconfig", ".npmrc":
		return true
	}
	if path.Base(s) == "config.fish" && strings.HasSuffix(path.Dir(s), "/.config/fish") {
		return true
	}
	if s == "/etc/bash.bashrc" || strings.HasPrefix(s, "/etc/profile") {
		return true
	}
	if strings.HasPrefix(s, "/etc/cron") || s == "/var/spool/cron" || strings.HasPrefix(s, "/var/spool/cron/") {
		return true
	}
	if s == "/etc/systemd" || strings.HasPrefix(s, "/etc/systemd/") {
		return true
	}
	if strings.HasSuffix(s, "/.config/systemd") || strings.Contains(s, "/.config/systemd/") {
		return true
	}
	if strings.HasSuffix(s, "/.pip") || strings.Contains(s, "/.pip/") {
		return true
	}
	if strings.HasSuffix(s, "/.config/autostart") || strings.Contains(s, "/.config/autostart/") {
		return true
	}
	return false
}

func diskDevice(abs string) bool {
	s := toSlash(abs)
	if !strings.HasPrefix(s, "/dev/") {
		return false
	}
	base := path.Base(s)
	switch {
	case strings.HasPrefix(base, "nvme"),
		strings.HasPrefix(base, "mmcblk"),
		strings.HasPrefix(base, "xvd"),
		strings.HasPrefix(base, "loop"),
		strings.HasPrefix(base, "dm-"),
		strings.HasPrefix(base, "nbd"),
		strings.HasPrefix(base, "disk"):
		return true
	}
	// sda, sda1, hda, vda — not stderr/stdin.
	if len(base) >= 3 && (strings.HasPrefix(base, "sd") || strings.HasPrefix(base, "hd") || strings.HasPrefix(base, "vd")) {
		c := base[2]
		return c >= 'a' && c <= 'z'
	}
	if strings.HasPrefix(base, "md") && len(base) > 2 && base[2] >= '0' && base[2] <= '9' {
		return true
	}
	return false
}

func diskGlob(raw string) bool {
	s := toSlash(strings.TrimSpace(raw))
	switch {
	case strings.HasPrefix(s, "/dev/sd"),
		strings.HasPrefix(s, "/dev/nvme"),
		strings.HasPrefix(s, "/dev/disk"),
		strings.HasPrefix(s, "/dev/mmcblk"),
		strings.HasPrefix(s, "/dev/xvd"),
		strings.HasPrefix(s, "/dev/vd"),
		strings.HasPrefix(s, "/dev/hd"):
		return true
	default:
		return false
	}
}

func harmlessDevice(abs string) bool {
	switch toSlash(abs) {
	case "/dev/null", "/dev/zero", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/stdin":
		return true
	default:
		return strings.HasPrefix(toSlash(abs), "/dev/fd/")
	}
}

// criticalTops are wiped by a recursive rm. Documented in the package comment.
var criticalTops = map[string]bool{
	"/bin": true, "/boot": true, "/dev": true, "/etc": true, "/home": true,
	"/lib": true, "/lib32": true, "/lib64": true, "/libx32": true,
	"/opt": true, "/proc": true, "/root": true, "/run": true, "/sbin": true,
	"/sys": true, "/usr": true, "/var": true, "/tmp": true, "/mnt": true,
	"/media": true, "/srv": true,
	"/System": true, "/Users": true, "/Applications": true, "/Volumes": true,
	"/private": true, "/Library": true,
}

// windowsCritical is a local delete of a drive root or of Windows itself.
// Remote paths are POSIX and never come here.
func windowsCritical(p string) bool {
	if !isWinPath(p) {
		return false
	}
	s := strings.ToLower(toSlash(cleanWin(p)))
	if len(s) < 2 || s[1] != ':' {
		return false
	}
	rest := strings.Trim(s[2:], "/")
	return rest == "" || rest == "windows" || strings.HasPrefix(rest, "windows/")
}

func criticalRaw(s string) bool {
	switch strings.TrimSpace(s) {
	case "/", "/*", "~", "~/", "$HOME", "${HOME}", "$HOME/", "${HOME}/",
		"~/*", "$HOME/*", "${HOME}/*":
		return true
	default:
		return false
	}
}

func dirPrefix(abs string) string {
	s := toSlash(abs)
	if isWinPath(abs) {
		s = toSlash(cleanWin(abs))
	}
	d := path.Dir(s)
	if d == "/" || d == "." || d == "" {
		return s
	}
	return d
}

func nameStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func nameCont(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
