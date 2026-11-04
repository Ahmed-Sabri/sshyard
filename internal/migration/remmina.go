package migration

import (
	"net"
	"strings"
)

func parseRemmina(data []byte, name string) (Result, error) {
	sections, err := iniRead(data)
	if err != nil {
		return Result{}, err
	}
	for _, s := range sections {
		if !strings.EqualFold(s.name, "remmina") {
			continue
		}
		e := Entry{Name: s.get("name"), Address: s.get("server"), User: s.get("username"), Group: s.get("group"), Port: iniPort(s.get("port")), KeyPath: s.get("ssh_privatekey")}
		if e.Name == "" {
			e.Name = iniFilename(name)
		}
		if !strings.EqualFold(s.get("protocol"), "SSH") {
			return Result{Issues: []Issue{{Code: "unsupported-protocol", Name: e.Name, Field: "protocol"}}}, nil
		}
		if host, port, err := net.SplitHostPort(e.Address); err == nil {
			e.Address = host
			e.Port = iniPort(port)
		} else if strings.Count(e.Address, ":") == 1 {
			e.Port = -1
		}
		iniReport(&e, s, "encrypted-secret", "password", "ssh_passphrase", "ssh_tunnel_password", "ssh_tunnel_passphrase")
		iniReport(&e, s, "unsupported-field", "ssh_tunnel_enabled", "ssh_tunnel_server", "ssh_proxycommand", "ssh_tunnel_proxycommand", "exec", "notes", "tags", "ssh_forward_x11")
		return Result{Entries: []Entry{e}}, nil
	}
	return Result{}, ErrFormat
}
