package policy

import "strings"

// interpVersion is an interpreter asked only for its version, or php for
// its modules: nothing runs.
func interpVersion(name string, args []arg) bool {
	if len(args) != 2 || !args[1].ok {
		return false
	}
	switch args[1].text {
	case "-v", "-V", "--version":
		return true
	case "-m":
		return name == "php"
	}
	return false
}

// serverOpts walks a server binary's options. letters in test only check
// or describe the configuration, letters in flags are harmless with them,
// letters in values take an argument and signal (if any) sends a signal to
// the running server. Any other option or a bare operand is ok=false.
func serverOpts(args []arg, test, flags, values string, signal byte) (testing bool, sig string, ok bool) {
	for i := 1; i < len(args); i++ {
		if !args[i].ok || !strings.HasPrefix(args[i].text, "-") || args[i].text == "-" {
			return false, "", false
		}
		t := args[i].text[1:]
		for j := 0; j < len(t); j++ {
			c := t[j]
			switch {
			case strings.IndexByte(test, c) >= 0:
				testing = true
			case strings.IndexByte(flags, c) >= 0:
			case strings.IndexByte(values, c) >= 0 || signal != 0 && c == signal:
				v := t[j+1:]
				if v == "" {
					i++
					if i >= len(args) || !args[i].ok {
						return false, "", false
					}
					v = args[i].text
				}
				if c == signal {
					sig = v
				}
				j = len(t)
			default:
				return false, "", false
			}
		}
	}
	return testing, sig, true
}

func serverService(prefix string) partial {
	return partial{cat: "service", risk: RiskMedium, prefix: prefix, why: prefix}
}

// gradeWebServer: a configuration test or version is read-only, a signal or
// apachectl verb that starts, stops or reloads is Medium "service", and
// anything else is Unknown.
func gradeWebServer(name string, args []arg) partial {
	read := partial{cat: "read", risk: RiskReadOnly, prefix: name + " -t", why: name + " config test"}
	if name != "nginx" && len(args) == 2 && args[1].ok {
		switch args[1].text {
		case "configtest", "status", "fullstatus":
			return read
		case "start", "stop", "restart", "graceful", "graceful-stop":
			return serverService(name + " " + args[1].text)
		}
	}
	test, flags, values := "tTvV", "qh?", "cpge"
	signal := byte('s')
	if name != "nginx" {
		test, flags, values, signal = "tSMvVlL", "h", "DfCceE", 'k'
	}
	testing, sig, ok := serverOpts(args, test, flags, values, signal)
	switch {
	case !ok:
		return partial{cat: "unknown", risk: RiskUnknown, prefix: name, why: "unverified " + name + " arguments"}
	case sig != "":
		return serverService(name + " " + string([]byte{'-', signal}))
	case testing:
		return read
	default:
		return serverService(name)
	}
}

// gradePHPFPM: -t, -v and -m are read-only, -i (phpinfo, which prints the
// environment) is a sensitive read, and starting the daemon is a service.
func gradePHPFPM(name string, args []arg) partial {
	for _, a := range args[1:] {
		if a.ok && a.text == "-i" {
			return partial{cat: "sensitive-read", risk: RiskMedium, prefix: name + " -i", why: "phpinfo"}
		}
	}
	testing, _, ok := serverOpts(args, "tvm", "h", "cypgd", 0)
	switch {
	case !ok:
		return partial{cat: "unknown", risk: RiskUnknown, prefix: name, why: "unverified " + name + " arguments"}
	case testing:
		return partial{cat: "read", risk: RiskReadOnly, prefix: name + " -t", why: name + " config test"}
	default:
		return serverService(name)
	}
}
