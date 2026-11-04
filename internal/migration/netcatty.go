package migration

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"io"
	"net/url"
	"strings"
)

func parseNetcatty(data []byte, name string) (Result, error) {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) || bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		return parseNetcattyJSON(data, name)
	}
	reader := csv.NewReader(bytes.NewReader(data))
	header, err := reader.Read()
	if err != nil {
		return Result{}, ErrFormat
	}
	columns := map[string]int{}
	for i, h := range header {
		columns[h] = i
	}
	if _, ok := columns["Hostname/IP"]; !ok {
		return Result{}, ErrFormat
	}
	r := Result{}
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{}, ErrFormat
		}
		m := object{}
		for h, i := range columns {
			m[h] = row[i]
		}
		e := Entry{Name: str(m, "Label"), Address: str(m, "Hostname/IP"), User: str(m, "Username"), Port: port(m["Port"]), Group: str(m, "Groups")}
		protocol := str(m, "Protocol")
		if protocol != "" && !strings.EqualFold(protocol, "ssh") {
			r.Issues = append(r.Issues, Issue{Code: "unsupported-protocol", Name: e.Name, Field: "Protocol"})
			continue
		}
		kp, err := decodeCSVField(str(m, "KeyPath"), "keypath")
		if err != nil {
			return Result{}, err
		}
		e.KeyPath = keyPath(kp, name)
		pass, err := decodeCSVField(str(m, "Passphrase"), "passphrase")
		if err != nil {
			return Result{}, err
		}
		netcattySecrets(&e, object{"password": str(m, "Password"), "passphrase": pass})
		chain, err := decodeCSVField(str(m, "ProxyJump"), "proxyjump")
		if err != nil {
			return Result{}, err
		}
		if chain != "" {
			for _, part := range strings.Split(chain, ",") {
				h, u, p := target(strings.TrimSpace(part))
				if p == 0 {
					p = 22
				}
				e.Jumps = append(e.Jumps, Hop{Address: h, User: u, Port: p})
			}
		}
		unsupported(&e, m, "Tags", "Notes", "Proxy", "LocalShell", "SshInputCompatibility")
		r.Entries = append(r.Entries, e)
	}
	return r, nil
}
func decodeCSVField(s, field string) (string, error) {
	prefix := "__netcatty_csv_" + field + "_v1__:"
	if !strings.HasPrefix(s, prefix) {
		return s, nil
	}
	v, err := url.PathUnescape(strings.TrimPrefix(s, prefix))
	if err != nil {
		return "", ErrFormat
	}
	return v, nil
}
func parseNetcattyJSON(data []byte, name string) (Result, error) {
	v, err := decodeJSON(data)
	if err != nil {
		return Result{}, err
	}
	root := obj(v)
	rows, ok := root["hosts"].([]any)
	if !ok {
		return Result{}, ErrFormat
	}
	// Encrypted backup envelopes have no host array and cannot be read as metadata.
	r := Result{}
	for _, row := range rows {
		m := obj(row)
		if m == nil {
			return Result{}, ErrFormat
		}
		if _, ok := m["hostname"]; !ok {
			return Result{}, ErrFormat
		}
		e := Entry{ID: str(m, "id"), Name: str(m, "label"), Address: str(m, "hostname"), User: str(m, "username"), Port: mapPort(m, "port"), Group: str(m, "group")}
		protocol := str(m, "protocol")
		if protocol != "" && !strings.EqualFold(protocol, "ssh") {
			r.Issues = append(r.Issues, Issue{Code: "unsupported-protocol", Name: e.Name, Field: "protocol"})
			continue
		}
		netcattySecrets(&e, m)
		if keys := arr(m["identityFilePaths"]); len(keys) > 0 {
			if p, ok := keys[0].(string); ok {
				e.KeyPath = keyPath(p, name)
			}
			if len(keys) > 1 {
				issue(&e, "unsupported-field", "identityFilePaths")
			}
		}
		for _, id := range arr(obj(m["hostChain"])["hostIds"]) {
			if s, ok := id.(string); ok {
				e.Jumps = append(e.Jumps, Hop{Ref: s})
			}
		}
		readNetcattyCredentials(&e, m, root, name)
		unsupported(&e, m, "tags", "notes", "proxy", "proxyConfig", "connectScripts", "proxyProfileId", "forwardedPorts")
		if len(arr(root["groupConfigs"])) > 0 {
			issue(&e, "unsupported-field", "groupConfigs")
		}
		r.Entries = append(r.Entries, e)
	}
	return r, nil
}

func readNetcattyCredentials(e *Entry, m, root object, name string) {
	keyID := str(m, "identityFileId")
	auth := str(m, "authMethod")
	if id := str(m, "identityId"); id != "" {
		identity, ok := findRecord(arr(root["identities"]), id)
		if !ok {
			issue(e, "missing-reference", "identityId")
		} else {
			if user := str(identity, "username"); user != "" {
				e.User = user
			}
			if method := str(identity, "authMethod"); method != "" {
				auth = method
			}
			netcattySecrets(e, identity)
			if id := str(identity, "keyId"); id != "" {
				keyID = id
			}
		}
	}
	if auth == "password" {
		e.PrivateKey = nil
		e.KeyPath = ""
		e.Passphrase = nil
		return
	}
	if auth == "certificate" {
		issue(e, "unsupported-field", "authMethod")
	}
	if m["useSshAgent"] == true {
		issue(e, "unsupported-field", "useSshAgent")
	}
	if keyID != "" {
		key, ok := findRecord(arr(root["keys"]), keyID)
		if !ok {
			issue(e, "missing-reference", "identityFileId")
		} else {
			netcattySecrets(e, key)
			if path := str(key, "filePath"); path != "" {
				e.KeyPath = keyPath(path, name)
			}
			unsupported(e, key, "certificate")
		}
	}
}
func findRecord(rows []any, id string) (object, bool) {
	var found object
	count := 0
	for _, row := range rows {
		m := obj(row)
		if str(m, "id") == id {
			found = m
			count++
		}
	}
	return found, count == 1
}

// Device ciphertext uses Chromium OSCrypt or DPAPI headers, not just a prefix.
func netcattyEncrypted(s string) bool {
	if !strings.HasPrefix(s, "enc:v1:") {
		return false
	}
	payload := strings.TrimPrefix(s, "enc:v1:")
	for _, r := range payload {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=') {
			return false
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(payload)
	}
	if err != nil {
		return false
	}
	if bytes.HasPrefix(decoded, []byte("v10")) || bytes.HasPrefix(decoded, []byte("v11")) {
		return len(decoded) >= 31 || len(decoded) >= 19 && (len(decoded)-3)%16 == 0
	}
	prefix := []byte{1, 0, 0, 0, 0xd0, 0x8c, 0x9d, 0xdf, 1, 0x15, 0xd1, 0x11, 0x8c, 0x7a, 0, 0xc0, 0x4f, 0xc2, 0x97, 0xeb}
	return len(decoded) > len(prefix) && bytes.HasPrefix(decoded, prefix)
}
func netcattySecrets(e *Entry, m object) {
	readable := object{}
	for _, f := range []string{"password", "privateKey", "passphrase"} {
		v, exists := m[f]
		if !exists || v == nil {
			continue
		}
		s := str(m, f)
		switch f {
		case "password":
			e.Password = nil
		case "privateKey":
			e.PrivateKey = nil
		case "passphrase":
			e.Passphrase = nil
		}
		if netcattyEncrypted(s) {
			issue(e, "encrypted-secret", f)
		} else {
			readable[f] = s
		}
	}
	secrets(e, readable, false)
}
