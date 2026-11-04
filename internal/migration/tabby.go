package migration

import (
	"bytes"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

func mergeMap(base, over object) object {
	out := object{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if m, ok := v.(map[string]any); ok {
			out[k] = mergeMap(obj(out[k]), m)
		} else {
			out[k] = v
		}
	}
	return out
}
func parseTabby(data []byte, name string) (Result, error) {
	var root object
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if dec.Decode(&root) != nil {
		return Result{}, ErrFormat
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return Result{}, ErrFormat
	}
	rows, ok := root["profiles"].([]any)
	if !ok {
		return Result{}, ErrFormat
	}
	groups := map[string]object{}
	for _, g := range arr(root["groups"]) {
		m := obj(g)
		id := str(m, "id")
		if m == nil || id == "" || groups[id] != nil {
			return Result{}, ErrFormat
		}
		groups[id] = m
	}
	r := Result{}
	for _, row := range rows {
		profile := obj(row)
		if profile == nil {
			return Result{}, ErrFormat
		}
		if str(profile, "type") != "ssh" {
			r.Issues = append(r.Issues, Issue{Code: "unsupported-protocol", Name: str(profile, "name"), Field: "type"})
			continue
		}
		gid := str(profile, "group")
		group := groups[gid]
		merged := mergeMap(obj(obj(root["profileDefaults"])["ssh"]), obj(obj(group["defaults"])["ssh"]))
		merged = mergeMap(merged, profile)
		options := obj(merged["options"])
		e := Entry{ID: str(profile, "id"), Name: str(profile, "name"), Address: str(options, "host"), User: str(options, "user"), Port: mapPort(options, "port")}
		seen := map[string]bool{}
		for gid != "" {
			if seen[gid] {
				issue(&e, "missing-reference", "group")
				break
			}
			seen[gid] = true
			g := groups[gid]
			if g == nil {
				e.Group = strings.Trim(gid+"/"+e.Group, "/")
				issue(&e, "missing-reference", "group")
				break
			}
			e.Group = strings.Trim(str(g, "name")+"/"+e.Group, "/")
			gid = str(g, "parentGroupId")
		}
		if keys := arr(options["privateKeys"]); len(keys) > 0 {
			if s, ok := keys[0].(string); ok {
				e.KeyPath = keyPath(s, name)
			}
			if len(keys) > 1 {
				issue(&e, "unsupported-field", "privateKeys")
			}
		}
		secrets(&e, options, false)
		// Tabby stores saved credentials in its vault or OS keychain, outside profiles.
		if len(e.Password) == 0 && str(options, "auth") == "password" {
			issue(&e, "encrypted-secret", "password")
		}
		switch str(options, "auth") {
		case "agent":
			e.Password = nil
			e.KeyPath = ""
			e.PrivateKey = nil
			e.Passphrase = nil
			issue(&e, "unsupported-field", "auth")
		case "password":
			e.KeyPath = ""
			e.PrivateKey = nil
			e.Passphrase = nil
		case "publicKey":
			e.Password = nil
			if e.KeyPath == "" {
				issue(&e, "unsupported-field", "auth")
			}
		}
		if ref := str(options, "jumpHost"); ref != "" {
			e.Jumps = append(e.Jumps, Hop{Ref: ref})
		}
		unsupported(&e, options, "proxyCommand", "forwardedPorts", "socksProxyHost", "httpProxyHost", "scripts")
		r.Entries = append(r.Entries, e)
	}
	return r, nil
}
