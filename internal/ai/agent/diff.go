package agent

import (
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines a hunk keeps on each side.
const diffContext = 3

// Unified is a unified diff of oldText and newText for path. A new file
// (oldText empty) is the full new text. Identical text is empty.
// Unchanged runs are collapsed to diffContext lines around each change.
func Unified(path, oldText, newText string) string {
	if oldText == "" {
		return newText
	}
	if oldText == newText {
		return ""
	}
	a, aNL := diffLines(oldText)
	b, bNL := diffLines(newText)
	edits := lineDiff(a, b)
	return formatDiff(path, edits, !aNL, !bNL)
}

// OnlyChanges drops the unchanged context lines of a unified diff.
func OnlyChanges(diff string) string {
	var b strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, " ") {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	if strings.HasSuffix(diff, "\n") && b.Len() > 0 {
		b.WriteByte('\n')
	}
	return b.String()
}

func diffLines(s string) (lines []string, nl bool) {
	if s == "" {
		return nil, true
	}
	nl = strings.HasSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n"), nl
}

type diffEdit struct {
	op byte // ' ', '-', '+'
	s  string
}

// lineDiff is an LCS diff. A file pair whose product of lengths exceeds
// the bound is one deletion hunk plus one insertion hunk.
// shortcut: the bound is 250_000 cells, raise it if large writes show a useless diff.
func lineDiff(a, b []string) []diffEdit {
	const bound = 250_000
	if len(a)*len(b) > bound {
		var out []diffEdit
		for _, s := range a {
			out = append(out, diffEdit{'-', s})
		}
		for _, s := range b {
			out = append(out, diffEdit{'+', s})
		}
		return out
	}
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []diffEdit
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			out = append(out, diffEdit{' ', a[i]})
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			out = append(out, diffEdit{'-', a[i]})
			i++
			continue
		}
		out = append(out, diffEdit{'+', b[j]})
		j++
	}
	for ; i < n; i++ {
		out = append(out, diffEdit{'-', a[i]})
	}
	for ; j < m; j++ {
		out = append(out, diffEdit{'+', b[j]})
	}
	return out
}

func formatDiff(path string, edits []diffEdit, oldMissing, newMissing bool) string {
	show := make([]bool, len(edits))
	for i, e := range edits {
		if e.op == ' ' {
			continue
		}
		from := i - diffContext
		if from < 0 {
			from = 0
		}
		to := i + diffContext
		if to >= len(edits) {
			to = len(edits) - 1
		}
		for j := from; j <= to; j++ {
			show[j] = true
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	oi, ni := 1, 1
	for i := 0; i < len(edits); {
		if !show[i] {
			if edits[i].op != '+' {
				oi++
			}
			if edits[i].op != '-' {
				ni++
			}
			i++
			continue
		}
		start, so, sn := i, oi, ni
		oc, nc := 0, 0
		for i < len(edits) && show[i] {
			if edits[i].op != '+' {
				oc++
				oi++
			}
			if edits[i].op != '-' {
				nc++
				ni++
			}
			i++
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", so, oc, sn, nc)
		for _, e := range edits[start:i] {
			b.WriteByte(e.op)
			b.WriteString(e.s)
			b.WriteByte('\n')
		}
	}
	if oldMissing {
		b.WriteString("\\ No newline at end of old file\n")
	}
	if newMissing {
		b.WriteString("\\ No newline at end of new file\n")
	}
	return b.String()
}
