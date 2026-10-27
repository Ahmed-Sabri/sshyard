package acp

import (
	"sort"
	"strings"
)

// deniedExact is stripped even when the agent's own config sets it.
var deniedExact = map[string]struct{}{
	"SSH_AUTH_SOCK":                     {},
	"SSH_AGENT_PID":                     {},
	"AWS_ACCESS_KEY_ID":                 {},
	"AWS_SECRET_ACCESS_KEY":             {},
	"AWS_SESSION_TOKEN":                 {},
	"AWS_SECURITY_TOKEN":                {},
	"AWS_SHARED_CREDENTIALS_FILE":       {},
	"GOOGLE_APPLICATION_CREDENTIALS":    {},
	"GOOGLE_CREDENTIALS":                {},
	"CLOUDSDK_AUTH_ACCESS_TOKEN":        {},
	"AZURE_CLIENT_SECRET":               {},
	"AZURE_CLIENT_CERTIFICATE_PATH":     {},
	"AZURE_CLIENT_CERTIFICATE_PASSWORD": {},
	"ARM_ACCESS_KEY":                    {},
	"ARM_CLIENT_SECRET":                 {},
	"DIGITALOCEAN_ACCESS_TOKEN":         {},
	"DIGITALOCEAN_TOKEN":                {},
	"ALICLOUD_ACCESS_KEY_ID":            {},
	"ALICLOUD_ACCESS_KEY_SECRET":        {},
	"TENCENTCLOUD_SECRET_ID":            {},
	"TENCENTCLOUD_SECRET_KEY":           {},
	"OCI_CLI_KEY_CONTENT":               {},
	"IBM_CLOUD_API_KEY":                 {},
}

func deniedName(name string) bool {
	if name == "" || strings.ContainsAny(name, "=\x00") {
		return true
	}
	if _, ok := deniedExact[name]; ok {
		return true
	}
	return strings.HasPrefix(name, "SSHYARD_")
}

func allowedBase(name string) bool {
	switch name {
	case "PATH", "HOME", "TMPDIR", "TEMP", "TMP", "USER", "USERNAME", "LOGNAME",
		"USERPROFILE", "LANG", "TERM", "COLORTERM", "TERMINFO", "TERMINFO_DIRS",
		"TERM_PROGRAM", "TERM_PROGRAM_VERSION", "SystemRoot", "WINDIR", "COMSPEC",
		"PATHEXT", "HOMEDRIVE", "HOMEPATH":
		return true
	default:
		return strings.HasPrefix(name, "LC_")
	}
}

// FilterEnv rebuilds a process environment. base is KEY=VALUE entries
// (os.Environ). extra is the agent's configured variables; denied names
// are still removed. extra wins on conflict. Order is base order, then
// extra names sorted.
func FilterEnv(base []string, extra map[string]string) []string {
	out := make([]string, 0, len(base))
	index := map[string]int{}
	for _, kv := range base {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || deniedName(name) || !allowedBase(name) {
			continue
		}
		if _, seen := index[name]; seen {
			continue
		}
		index[name] = len(out)
		out = append(out, name+"="+val)
	}
	names := make([]string, 0, len(extra))
	for name := range extra {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if deniedName(name) || strings.Contains(extra[name], "\x00") {
			continue
		}
		kv := name + "=" + extra[name]
		if i, ok := index[name]; ok {
			out[i] = kv
			continue
		}
		index[name] = len(out)
		out = append(out, kv)
	}
	return out
}
