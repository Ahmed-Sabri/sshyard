package migration

import (
	"net/url"
	"path"
	"strings"
)

// Protocol numbers follow WinSCP's TFSProtocol enum, not URL scheme indexes.
func parseWinSCP(data []byte, name string) (Result, error) {
	sections, err := iniRead(data)
	if err != nil {
		return Result{}, err
	}
	r := Result{}
	found := false
	for _, s := range sections {
		if !strings.HasPrefix(strings.ToLower(s.name), "sessions\\") {
			continue
		}
		found = true
		decoded, err := url.PathUnescape(s.name[len("Sessions\\"):])
		if err != nil {
			return Result{}, ErrFormat
		}
		decoded = strings.ReplaceAll(decoded, "\\", "/")
		e := Entry{Name: path.Base(decoded), Group: path.Dir(decoded), Address: s.get("HostName"), User: s.get("UserName"), Port: iniPort(s.get("PortNumber")), KeyPath: s.get("PublicKeyFile")}
		if e.Group == "." {
			e.Group = ""
		}
		p := s.get("FSProtocol")
		if p != "" && p != "0" && p != "1" && p != "2" {
			r.Issues = append(r.Issues, Issue{Code: "unsupported-protocol", Name: e.Name, Field: "FSProtocol"})
			continue
		}
		if e.Name == "Default Settings" && e.Address == "" {
			continue
		}
		iniReport(&e, s, "encrypted-secret", "Password", "PasswordPlain", "Passphrase", "PassphrasePlain", "ProxyPassword", "TunnelPassword")
		iniReport(&e, s, "unsupported-field", "ProxyMethod", "Tunnel", "TunnelHostName", "Note", "Notes", "Tags", "RemoteDirectory")
		r.Entries = append(r.Entries, e)
	}
	if !found {
		return r, ErrFormat
	}
	return r, nil
}
