package policy

import "strings"

// Only verified option forms may retain the automatic read-only grant, since
// familiar reader programs also have execution, output and configuration flags.
func verifiedReadArgs(name string, args []arg) bool {
	if name == "echo" || name == "printf" {
		return true
	}
	short, values := "", ""
	long := "help version"
	switch name {
	case "ls":
		short = "aAbBcCdDfFgGhHiIklLmMnNopqQrRsStTuUvwxX1"
		long += " all almost-all directory human-readable long reverse recursive"
	case "cat":
		short = "AbEnstTuv"
	case "head", "tail":
		short, values = "qv", "nc"
	case "grep", "egrep", "fgrep", "rg":
		short, values = "EFGivwxonHhclLqsrRzZUabIP", "efmABC"
		long += " fixed-strings ignore-case line-number files-with-matches count word-regexp line-regexp recursive no-heading hidden no-ignore"
	case "sort":
		short, values = "bdfgMhnRrsuVzmcC", "okStT"
		long += " reverse unique numeric-sort stable output="
	case "uniq":
		short, values = "cduiz", "fsw"
	case "wc":
		short = "clmwL"
	case "less", "more":
		short = "EFGIMNQRSTUVWXceginqrsuw"
	case "df", "du":
		short = "ahHiklmPstx"
	case "free":
		short = "bhkmgwt"
	case "uname":
		short = "asnrvmpio"
	case "ps":
		short = "aefjluxw"
	case "top":
		short, values = "bHic", "ndp"
	case "journalctl":
		short, values = "brxeqf", "nu"
		long += " no-pager"
	case "ss", "netstat":
		short = "alntupxro46"
	case "stat", "file":
		short = "Lbih"
	case "id":
		short = "Gnruzg"
	case "pwd":
		short = "LP"
	case "date":
		short = "uR"
	case "printenv":
		short = "0"
	}
	operands, options := 0, true
	for i := 1; i < len(args); i++ {
		if !args[i].ok {
			continue
		}
		t := args[i].text
		if options && t == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(t, "--") {
			key, _, attached := strings.Cut(t[2:], "=")
			if strings.Contains(" "+long+" ", " "+key+"= ") {
				if !attached {
					i++
					if i >= len(args) {
						return false
					}
				}
				continue
			}
			if attached || !strings.Contains(" "+long+" ", " "+key+" ") {
				return false
			}
			continue
		}
		if options && strings.HasPrefix(t, "-") && t != "-" {
			for j, c := range t[1:] {
				if strings.ContainsRune(values, c) {
					if j+2 == len(t) {
						i++
						if i >= len(args) {
							return false
						}
					}
					break
				}
				if !strings.ContainsRune(short, c) {
					return false
				}
			}
			continue
		}
		operands++
		if (name == "less" || name == "more") && strings.HasPrefix(t, "+") {
			return false
		}
		if name == "date" && !strings.HasPrefix(t, "+") {
			return false
		}
	}
	// uniq's second operand is an output file; hostname's operand changes it.
	return !(name == "uniq" && operands > 1 || name == "hostname" && operands > 0)
}

func recursiveRead(name string, args []arg) bool {
	if name != "grep" && name != "egrep" && name != "fgrep" {
		return false
	}
	for _, a := range args[1:] {
		if a.ok && (a.text == "--recursive" || strings.HasPrefix(a.text, "-") && !strings.HasPrefix(a.text, "--") && strings.ContainsAny(a.text[1:], "rR")) {
			return true
		}
	}
	return false
}

func shellReadsContent(name string, args []arg) bool {
	if helpOnly(args) {
		return false
	}
	switch name {
	case "cat", "head", "tail", "less", "more", "grep", "rg", "egrep", "fgrep", "sort", "uniq", "wc", "file", "sed":
		// A shell operand can resolve through a link or glob; only the built-in
		// file tools can verify actual SFTP paths before automatic reads.
		if name == "grep" || name == "egrep" || name == "fgrep" {
			operands := 0
			for _, a := range args[1:] {
				if !a.ok {
					return true
				}
				if strings.HasPrefix(a.text, "-") {
					return true
				}
				operands++
			}
			return operands > 1
		}
		return len(args) > 1
	}
	return false
}

func knownCommand(name string) bool {
	if readOnlyCmd(name) || isWrap(name) || isInterp(name) {
		return true
	}
	switch name {
	case "rm", "rmdir", "unlink", "truncate", "shred", "mkdir", "touch", "mv", "cp", "ln", "install", "tee", "sed", "find", "chmod", "chown", "chgrp", "dd", "systemctl", "service", "docker", "git", "kubectl", "ip", "reboot", "shutdown":
		return true
	}
	return false
}

func processEnvironment(name string, args []arg) bool {
	if name != "ps" {
		return false
	}
	for _, a := range args[1:] {
		if a.ok && !strings.HasPrefix(a.text, "-") && strings.Contains(a.text, "e") {
			return true
		}
	}
	return false
}
