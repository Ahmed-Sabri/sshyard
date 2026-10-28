package acp

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// errInvalid is a rejected number, such as a negative line or byte limit.
var errInvalid = errors.New("invalid value")

const (
	stderrCap        = 64 << 10
	crashTail        = 4 << 10
	readCap          = 10 << 20
	writeCap         = 10 << 20
	defaultTermLimit = 1 << 20
	maxTermLimit     = 1 << 20
	maxCmdLen        = 64 << 10
	maxTerminals     = 2
	reasonCap        = 500
)

// TrimTail keeps the last limit bytes, starting on a UTF-8 boundary.
// A non-positive limit keeps nothing.
func TrimTail(s string, limit int) string {
	if limit <= 0 || s == "" {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	b := s[len(s)-limit:]
	for len(b) > 0 && !utf8.RuneStart(b[0]) {
		b = b[1:]
	}
	return b
}

func termLimit(p *int) (int, error) {
	if p == nil {
		return defaultTermLimit, nil
	}
	if *p < 0 {
		return 0, errInvalid
	}
	if *p > maxTermLimit {
		return maxTermLimit, nil
	}
	return *p, nil
}

// sliceLines applies a 1-based line and a line limit. A nil line starts
// at the first line. A nil limit keeps the rest.
func sliceLines(s string, line, limit *int) (string, error) {
	if line != nil && *line < 1 {
		return "", errInvalid
	}
	if limit != nil && *limit < 0 {
		return "", errInvalid
	}
	lines := strings.Split(s, "\n")
	start := 0
	if line != nil {
		start = *line - 1
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := len(lines)
	if limit != nil {
		end = start + min(*limit, len(lines)-start)
	}
	return strings.Join(lines[start:end], "\n"), nil
}

func clip(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return TrimTail(s, n)
}
