// Package sshconfig reads OpenSSH client configuration (~/.ssh/config)
// for import: the Host blocks, with the directives SSHYard keeps (HostName,
// User, Port, IdentityFile, ProxyJump), Include, and the defaults of
// pattern blocks such as Host *. It only reads files; it never writes.
package sshconfig

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// maxDepth bounds nested Includes, as ssh does.
const maxDepth = 16

// Env is what token expansion needs: the local home and user name.
type Env struct {
	Home      string
	LocalUser string
}

// Hop is one host of a ProxyJump: [user@]host[:port].
type Hop struct {
	User string
	Host string
	Port int // 0: not given
}

func (h Hop) String() string {
	s := h.Host
	if strings.Contains(s, ":") {
		s = "[" + s + "]"
	}
	if h.User != "" {
		s = h.User + "@" + s
	}
	if h.Port != 0 {
		s += ":" + strconv.Itoa(h.Port)
	}
	return s
}

// Entry is one host of the config as ssh would resolve it, defaults of
// matching pattern blocks included.
type Entry struct {
	// Alias is the name on the Host line.
	Alias string
	// HostName is where to connect, the alias when the config names none.
	HostName string
	User     string
	Port     int // 0: not given (22)
	// IdentityFiles are the key files, expanded to absolute paths, in order.
	IdentityFiles []string
	// ProxyJump is the jump chain, first hop first; nil for none.
	ProxyJump []Hop
	// Unsupported names the directives applying to the host that are not
	// imported, as ProxyCommand or LocalForward, each once.
	Unsupported []string
	// Pattern reports a Host of wildcards (*.corp): it names no one host and
	// is skipped. Host * itself gives defaults and is no entry.
	Pattern bool
	// File and Line are where the Host line is.
	File string
	Line int
}

// Config is a parsed config.
type Config struct {
	env    Env
	blocks []block
	// Entries are the config's hosts in order, patterns included (marked),
	// Host * left out.
	Entries []Entry
	// Errors are the lines that could not be read, as "file:line: why";
	// they do not stop the parse.
	Errors []string
	places map[string]where
}

type directive struct {
	key, val string
}

type block struct {
	// patterns are the Host line's patterns; nil applies to every host
	// (lines before the first Host). match marks a Match block, which
	// applies to none since it is not supported.
	patterns []string
	match    bool
	dirs     []directive
}

// keep are the directives imported, by lower-case name.
var keep = map[string]string{
	"hostname": "HostName", "user": "User", "port": "Port",
	"identityfile": "IdentityFile", "proxyjump": "ProxyJump",
}

// Parse reads the config at path and the files it includes.
func Parse(path string, env Env) (*Config, error) {
	c := &Config{env: env}
	c.blocks = []block{{}}
	if err := c.read(path, 0, true); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, b := range c.blocks {
		if b.match {
			continue
		}
		for _, p := range b.patterns {
			if p == "*" || strings.HasPrefix(p, "!") || seen[p] {
				continue
			}
			seen[p] = true
			pattern := strings.ContainsAny(p, "*?")
			var e Entry
			if pattern {
				w := c.places[p]
				e = Entry{Alias: p, Pattern: true, File: w.file, Line: w.line}
			} else {
				e = c.Resolve(p)
			}
			c.Entries = append(c.Entries, e)
		}
	}
	return c, nil
}

// where records the place of each Host line for Entry.File and Line.
type where struct {
	file string
	line int
}

func (c *Config) read(path string, depth int, top bool) error {
	if depth > maxDepth {
		return fmt.Errorf("sshconfig: includes nested too deep at %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		if top {
			return err
		}
		return nil // a missing include is skipped, as ssh does
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		key, args, err := split(sc.Text())
		if err != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("%s:%d: %v", path, n, err))
			continue
		}
		if key == "" {
			continue
		}
		switch strings.ToLower(key) {
		case "host":
			c.blocks = append(c.blocks, block{patterns: args})
			if c.places == nil {
				c.places = map[string]where{}
			}
			for _, p := range args {
				if _, ok := c.places[p]; !ok {
					c.places[p] = where{path, n}
				}
			}
		case "match":
			c.blocks = append(c.blocks, block{match: true})
		case "include":
			cur := c.blocks[len(c.blocks)-1]
			for _, a := range args {
				for _, file := range c.globInclude(a) {
					if err := c.read(file, depth+1, false); err != nil {
						return err
					}
				}
			}
			// What follows the Include belongs to the block it was in.
			c.blocks = append(c.blocks, block{patterns: cur.patterns, match: cur.match})
		default:
			b := &c.blocks[len(c.blocks)-1]
			b.dirs = append(b.dirs, directive{key, strings.Join(args, " ")})
		}
	}
	return sc.Err()
}

// globInclude expands an Include argument: ~ and relative paths are from
// ~/.ssh, globs are sorted.
func (c *Config) globInclude(arg string) []string {
	p := c.tilde(arg)
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.env.Home, ".ssh", p)
	}
	files, err := filepath.Glob(p)
	if err != nil {
		return nil
	}
	sort.Strings(files)
	var out []string
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && !st.IsDir() {
			out = append(out, f)
		}
	}
	return out
}

func (c *Config) tilde(p string) string {
	if p == "~" {
		return c.env.Home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(c.env.Home, p[2:])
	}
	return p
}

// split cuts a line into its keyword and arguments: "Key value",
// "Key=value", quoted arguments, # comments.
func split(line string) (string, []string, error) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return "", nil, nil
	}
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return line, nil, nil
	}
	key, rest := line[:i], strings.TrimLeft(line[i:], " \t")
	if strings.HasPrefix(rest, "=") {
		rest = strings.TrimLeft(rest[1:], " \t")
	}
	var args []string
	for rest != "" {
		if rest[0] == '#' {
			break
		}
		var arg string
		if rest[0] == '"' {
			j := strings.IndexByte(rest[1:], '"')
			if j < 0 {
				return "", nil, errors.New("unterminated quote")
			}
			arg, rest = rest[1:1+j], rest[2+j:]
		} else {
			j := strings.IndexAny(rest, " \t")
			if j < 0 {
				j = len(rest)
			}
			arg, rest = rest[:j], rest[j:]
		}
		args = append(args, arg)
		rest = strings.TrimLeft(rest, " \t")
	}
	return key, args, nil
}

// Match reports whether name matches a Host line's patterns: one plain
// pattern matches and no negated one does.
func Match(patterns []string, name string) bool {
	ok := false
	for _, p := range patterns {
		if neg := strings.HasPrefix(p, "!"); neg {
			if glob(p[1:], name) {
				return false
			}
		} else if glob(p, name) {
			ok = true
		}
	}
	return ok
}

// glob matches * and ? case-insensitively, as ssh's match_pattern.
func glob(p, s string) bool {
	p, s = strings.ToLower(p), strings.ToLower(s)
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if p == "" {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if glob(p, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if s == "" {
				return false
			}
			p, s = p[1:], s[1:]
		default:
			if s == "" || p[0] != s[0] {
				return false
			}
			p, s = p[1:], s[1:]
		}
	}
	return s == ""
}

// Resolve is the configuration ssh would use for name: each directive
// from the first block matching it that sets it, IdentityFiles from all.
// It serves the jump hosts of ProxyJump too, which need not be on a Host
// line of their own.
func (c *Config) Resolve(name string) Entry {
	e := Entry{Alias: name}
	if w, ok := c.places[name]; ok {
		e.File, e.Line = w.file, w.line
	}
	set := map[string]bool{}
	unsup := map[string]bool{}
	var jump string
	for _, b := range c.blocks {
		if b.match || (b.patterns != nil && !Match(b.patterns, name)) {
			continue
		}
		for _, d := range b.dirs {
			k := strings.ToLower(d.key)
			canon, ok := keep[k]
			if !ok {
				if !unsup[k] {
					unsup[k] = true
					e.Unsupported = append(e.Unsupported, d.key)
				}
				continue
			}
			if k == "identityfile" {
				if strings.EqualFold(d.val, "none") {
					continue
				}
				e.IdentityFiles = append(e.IdentityFiles, d.val)
				continue
			}
			if set[k] {
				continue
			}
			set[k] = true
			switch canon {
			case "HostName":
				e.HostName = d.val
			case "User":
				e.User = d.val
			case "Port":
				if p, err := strconv.Atoi(d.val); err == nil && p > 0 && p < 65536 {
					e.Port = p
				}
			case "ProxyJump":
				jump = d.val
			}
		}
	}
	if e.HostName == "" {
		e.HostName = name
	}
	e.HostName = strings.ReplaceAll(e.HostName, "%h", name)
	if jump != "" && !strings.EqualFold(jump, "none") {
		e.ProxyJump = ParseJump(jump)
	}
	seen := map[string]bool{}
	var files []string
	for _, f := range e.IdentityFiles {
		f = c.expand(f, e)
		if !filepath.IsAbs(f) {
			f = filepath.Join(c.env.Home, f)
		}
		// %d pastes Home, whose separator may differ from the slashes
		// in the config, in front of the rest. Clean makes one path.
		f = filepath.Clean(f)
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	e.IdentityFiles = files
	return e
}

// expand replaces ~ and the tokens of IdentityFile: %d home, %u local
// user, %h host name, %r remote user, %n alias, %% a percent.
func (c *Config) expand(s string, e Entry) string {
	s = c.tilde(s)
	if !strings.Contains(s, "%") {
		return s
	}
	user := e.User
	if user == "" {
		user = c.env.LocalUser
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'd':
			b.WriteString(c.env.Home)
		case 'u':
			b.WriteString(c.env.LocalUser)
		case 'h':
			b.WriteString(e.HostName)
		case 'r':
			b.WriteString(user)
		case 'n':
			b.WriteString(e.Alias)
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// ParseJump reads a ProxyJump value: comma-separated [user@]host[:port],
// or ssh:// URIs of the same.
func ParseJump(s string) []Hop {
	var out []Hop
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "ssh://")
		if part == "" {
			continue
		}
		h, ok := ParseTarget(part)
		if ok {
			out = append(out, h)
		}
	}
	return out
}

// ParseTarget reads [user@]host[:port], the host an IPv6 address in
// brackets when it has a port.
func ParseTarget(s string) (Hop, bool) {
	var h Hop
	if i := strings.LastIndex(s, "@"); i >= 0 {
		h.User, s = s[:i], s[i+1:]
	}
	if strings.HasPrefix(s, "[") {
		j := strings.Index(s, "]")
		if j < 0 {
			return Hop{}, false
		}
		h.Host, s = s[1:j], s[j+1:]
		if strings.HasPrefix(s, ":") {
			p, err := strconv.Atoi(s[1:])
			if err != nil || p < 1 || p > 65535 {
				return Hop{}, false
			}
			h.Port = p
		} else if s != "" {
			return Hop{}, false
		}
	} else if i := strings.LastIndex(s, ":"); i >= 0 && strings.Count(s, ":") == 1 {
		p, err := strconv.Atoi(s[i+1:])
		if err != nil || p < 1 || p > 65535 {
			return Hop{}, false
		}
		h.Host, h.Port = s[:i], p
	} else {
		h.Host = s
	}
	if h.Host == "" || strings.ContainsAny(h.Host, " \t/") {
		return Hop{}, false
	}
	return h, true
}
