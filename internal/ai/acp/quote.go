package acp

import "strings"

// ShellQuote quotes one argument for a POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteCommand joins a command and its arguments as one safely quoted
// string. policy.Exec runs that string with sh -c; do not wrap it again.
func QuoteCommand(cmd string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, ShellQuote(cmd))
	for _, a := range args {
		parts = append(parts, ShellQuote(a))
	}
	return strings.Join(parts, " ")
}
