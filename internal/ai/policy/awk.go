package policy

import "strings"

func awkName(name string) bool {
	return name == "awk" || name == "gawk" || name == "mawk" || name == "nawk"
}

// namedUnk is Unknown with name as its grant prefix.
func namedUnk(name, why string) partial {
	u := unk(why)
	u.prefix = name
	return u
}

// gradeAwk is read-only when every program is visible and can neither run
// a command nor write a file, and every input file is a trusted read.
// -f, gawk's include and load options and any other option are Unknown.
func (e *shellEnv) gradeAwk(name string, args []arg) partial {
	var progs []string
	var files []arg
	options, sawProg := true, false
	for i := 1; i < len(args); i++ {
		if !args[i].ok {
			return namedUnk(name, "awk argument")
		}
		t := args[i].text
		switch {
		case !options || t == "-" || !strings.HasPrefix(t, "-"):
			options = false
			if !sawProg {
				progs = append(progs, t)
				sawProg = true
				continue
			}
			files = append(files, args[i])
		case t == "--":
			options = false
		case t == "-F" || t == "-v":
			if i+1 >= len(args) || !args[i+1].ok {
				return namedUnk(name, "awk "+t)
			}
			i++
		case strings.HasPrefix(t, "-F") || strings.HasPrefix(t, "-v") ||
			strings.HasPrefix(t, "--field-separator=") || strings.HasPrefix(t, "--assign="):
		case t == "-e" || t == "--source":
			if i+1 >= len(args) || !args[i+1].ok {
				return namedUnk(name, "awk "+t)
			}
			progs = append(progs, args[i+1].text)
			sawProg = true
			i++
		case strings.HasPrefix(t, "--source="):
			progs = append(progs, strings.TrimPrefix(t, "--source="))
			sawProg = true
		default:
			return namedUnk(name, "awk option "+t)
		}
	}
	if len(progs) == 0 {
		return namedUnk(name, "awk")
	}
	g := partial{cat: "read", risk: RiskReadOnly, prefix: name, why: name}
	for _, p := range progs {
		exec, env, bad := scanAwk(p)
		if bad || exec {
			return namedUnk(name, "awk program")
		}
		if env {
			g = better(g, partial{cat: "sensitive-read", risk: RiskMedium, why: "awk ENVIRON"})
		}
	}
	for _, f := range files {
		if f.text == "-" || isAssign(f) {
			continue
		}
		if e.sensitive(f.text) {
			g = better(g, sens(f.text))
		} else if !e.trustedRead(f.text) {
			g = better(g, partial{cat: "sensitive-read", risk: RiskMedium, why: "unresolved file content read"})
		}
	}
	g.prefix = name
	return g
}

// scanAwk walks one awk program with its strings, regex literals and
// comments removed. exec is anything that can run a command or write a
// file: system, getline, a pipe, an output redirection in print or printf,
// or a gawk @ directive or indirect call. env is a read of ENVIRON. bad
// means a literal does not end or a backslash is outside one.
func scanAwk(s string) (exec, env, bad bool) {
	var code strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			j := i + 1
			for ; j < len(s) && s[j] != '"'; j++ {
				if s[j] == '\\' {
					j++
				}
			}
			if j >= len(s) {
				return false, false, true
			}
			code.WriteString(`""`)
			i = j
		case c == '/' && awkRegexStart(code.String()):
			j, bracket := i+1, false
			for ; j < len(s); j++ {
				if s[j] == '\\' {
					j++
				} else if s[j] == '[' {
					bracket = true
				} else if s[j] == ']' {
					bracket = false
				} else if s[j] == '\n' {
					return false, false, true
				} else if s[j] == '/' && !bracket {
					break
				}
			}
			if j >= len(s) {
				return false, false, true
			}
			code.WriteByte('0')
			i = j
		case c == '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			code.WriteByte('\n')
		case c == '\\':
			// A line continuation would turn a / after it into division.
			return false, false, true
		default:
			code.WriteByte(c)
		}
	}
	src := code.String()
	depth, printDepth, inPrint := 0, 0, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if isAwkWord(c) {
			j := i
			for j < len(src) && isAwkWord(src[j]) {
				j++
			}
			switch src[i:j] {
			case "system", "getline":
				exec = true
			case "ENVIRON":
				env = true
			case "print", "printf":
				inPrint, printDepth = true, depth
			}
			i = j - 1
			continue
		}
		switch c {
		case '@':
			exec = true
		case '|':
			if i+1 < len(src) && src[i+1] == '|' {
				i++
				continue
			}
			exec = true
		case '>':
			if inPrint && depth == printDepth {
				exec = true
			}
		case '(':
			depth++
		case ')':
			depth--
		case ';', '\n', '{', '}':
			inPrint = false
		}
	}
	return exec, env, false
}

// awkRegexStart reports whether a / after code opens a regex literal. Only
// a position no operand can precede counts; anything else is division, so
// code is never hidden inside what looks like a regex.
func awkRegexStart(code string) bool {
	code = strings.TrimRight(code, " \t")
	if code == "" {
		return true
	}
	return strings.ContainsRune("(,~!{};&|?:\n", rune(code[len(code)-1]))
}

func isAwkWord(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
