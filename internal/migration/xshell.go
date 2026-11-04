package migration

import "strings"

// Xshell sessions store connection and authentication in separate INI sections.
func parseXshell(data []byte, name string) (Result, error) {
	sections, err := iniRead(data)
	if err != nil {
		return Result{}, err
	}
	e := Entry{Name: iniFilename(name)}
	found := false
	for _, s := range sections {
		switch strings.ToUpper(s.name) {
		case "CONNECTION":
			found = true
			protocol := s.get("Protocol")
			if !strings.EqualFold(protocol, "SSH") && !strings.EqualFold(protocol, "SSH2") {
				return Result{Issues: []Issue{{Code: "unsupported-protocol", Name: e.Name, Field: "Protocol"}}}, nil
			}
			e.Address = s.get("Host")
			e.Port = iniPort(s.get("Port"))
			iniReport(&e, s, "unsupported-field", "Description", "Notes", "Tags", "Proxy", "ProxyHost", "ProxyType")
		case "CONNECTION:AUTHENTICATION":
			e.User = s.get("UserName")
			e.KeyPath = s.get("UserKey")
			iniReport(&e, s, "encrypted-secret", "Password", "Passphrase")
		case "CONNECTION:PROXY", "CONNECTION:SSH:TUNNELING":
			for _, p := range s.pairs {
				iniReport(&e, s, "unsupported-field", p.key)
			}
		}
	}
	if !found {
		return Result{}, ErrFormat
	}
	return Result{Entries: []Entry{e}}, nil
}
