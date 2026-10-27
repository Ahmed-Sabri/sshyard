package sshconfig

import (
	"fmt"
	"strconv"
	"strings"
)

// ErrQuickOption is a command-line option quick connect does not take.
type ErrQuickOption struct{ Option string }

func (e *ErrQuickOption) Error() string { return "sshconfig: unsupported option " + e.Option }

// noArg are the ssh options without an argument, which quick connect
// ignores.
const noArg = "46AaCfGgKkMNnqsTtVvXxYy"

// ParseQuick reads what quick connect takes: user@host:port, host,
// ssh://user@host:port, or an ssh command as "ssh -p 2222 user@host"
// (-p and -l read, options without an argument ignored).
func ParseQuick(s string) (Hop, error) {
	f := strings.Fields(strings.TrimSpace(s))
	if len(f) == 0 {
		return Hop{}, fmt.Errorf("sshconfig: nothing to connect to")
	}
	if f[0] == "ssh" {
		f = f[1:]
	}
	var h Hop
	var dest string
	port, user := 0, ""
	for i := 0; i < len(f); i++ {
		a := f[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			if dest != "" {
				return Hop{}, fmt.Errorf("sshconfig: a remote command is not supported")
			}
			dest = a
			continue
		}
		opt, val := a[1:2], a[2:]
		switch {
		case opt == "p" || opt == "l":
			if val == "" {
				if i+1 == len(f) {
					return Hop{}, fmt.Errorf("sshconfig: -%s needs a value", opt)
				}
				i++
				val = f[i]
			}
			if opt == "l" {
				user = val
				break
			}
			p, err := strconv.Atoi(val)
			if err != nil || p < 1 || p > 65535 {
				return Hop{}, fmt.Errorf("sshconfig: invalid port %q", val)
			}
			port = p
		case strings.Contains(noArg, opt) && strings.Trim(a[1:], noArg) == "":
		default:
			return Hop{}, &ErrQuickOption{Option: "-" + opt}
		}
	}
	if dest == "" {
		return Hop{}, fmt.Errorf("sshconfig: no host")
	}
	dest = strings.TrimSuffix(strings.TrimPrefix(dest, "ssh://"), "/")
	h, ok := ParseTarget(dest)
	if !ok {
		// A bare IPv6 address, no brackets and no port.
		if strings.Count(dest, ":") > 1 && !strings.ContainsAny(dest, "[]") {
			at := strings.LastIndex(dest, "@")
			h = Hop{User: dest[:max(at, 0)], Host: dest[at+1:]}
		} else {
			return Hop{}, fmt.Errorf("sshconfig: invalid host %q", dest)
		}
	}
	if port != 0 {
		h.Port = port
	}
	if user != "" && h.User == "" {
		h.User = user
	}
	return h, nil
}
