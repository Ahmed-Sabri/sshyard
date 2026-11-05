package detect

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"time"
)

// systemProxy is the proxy the system settings name, as proxy variables:
// an app started from the desktop has the setting but not the variables
// a shell would export. Nil when there is none or it cannot be read.
func systemProxy(ctx context.Context, goos string) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	switch goos {
	case "darwin":
		out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
		if err != nil {
			return nil
		}
		return parseScutil(string(out))
	case "windows":
		out, err := exec.CommandContext(ctx, "reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`).Output()
		if err != nil {
			return nil
		}
		return parseInternetSettings(string(out))
	}
	return nil
}

// parseScutil reads `scutil --proxy`: the enabled HTTP, HTTPS and SOCKS
// proxies and the exceptions list.
func parseScutil(out string) map[string]string {
	v := map[string]string{}
	var exceptions []string
	inList := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if inList {
			if line == "}" {
				inList = false
			} else if _, item, ok := strings.Cut(line, " : "); ok {
				exceptions = append(exceptions, strings.TrimSpace(item))
			}
			continue
		}
		key, value, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "ExceptionsList" {
			inList = strings.HasPrefix(value, "<array>")
			continue
		}
		v[key] = value
	}
	proxy := func(kind, scheme string) string {
		if v[kind+"Enable"] != "1" || v[kind+"Proxy"] == "" || v[kind+"Port"] == "" {
			return ""
		}
		return scheme + "://" + net.JoinHostPort(v[kind+"Proxy"], v[kind+"Port"])
	}
	return proxyVars(proxy("HTTP", "http"), proxy("HTTPS", "http"), proxy("SOCKS", "socks5"), exceptions)
}

// parseInternetSettings reads the Internet Settings key `reg query`
// prints: ProxyEnable, ProxyServer ("host:port", or "http=host:port;
// https=...;socks=...") and ProxyOverride.
func parseInternetSettings(out string) map[string]string {
	v := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && strings.HasPrefix(f[1], "REG_") {
			v[f[0]] = strings.Join(f[2:], " ")
		}
	}
	if v["ProxyEnable"] != "0x1" || v["ProxyServer"] == "" {
		return nil
	}
	var httpProxy, httpsProxy, socks string
	server := v["ProxyServer"]
	if !strings.Contains(server, "=") {
		httpProxy, httpsProxy = "http://"+server, "http://"+server
	} else {
		for _, part := range strings.Split(server, ";") {
			kind, addr, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok || addr == "" {
				continue
			}
			switch strings.ToLower(kind) {
			case "http":
				httpProxy = "http://" + addr
			case "https":
				httpsProxy = "http://" + addr
			case "socks":
				socks = "socks5://" + addr
			}
		}
	}
	var exceptions []string
	for _, e := range strings.Split(v["ProxyOverride"], ";") {
		if e = strings.TrimSpace(e); e == "<local>" {
			exceptions = append(exceptions, "localhost", "127.0.0.1")
		} else {
			exceptions = append(exceptions, e)
		}
	}
	return proxyVars(httpProxy, httpsProxy, socks, exceptions)
}

// proxyVars are the variables for these proxies. NO_PROXY keeps the
// exceptions it can say: "*.example.com" becomes ".example.com", and other
// wildcards are left out.
func proxyVars(httpProxy, httpsProxy, socks string, exceptions []string) map[string]string {
	if httpProxy == "" && httpsProxy == "" && socks == "" {
		return nil
	}
	if httpsProxy == "" {
		httpsProxy = httpProxy
	}
	if httpProxy == "" {
		httpProxy = httpsProxy
	}
	v := map[string]string{}
	set := func(name, value string) {
		if value != "" {
			v[name], v[strings.ToLower(name)] = value, value
		}
	}
	set("HTTP_PROXY", httpProxy)
	set("HTTPS_PROXY", httpsProxy)
	if socks != "" {
		set("ALL_PROXY", socks)
	} else {
		set("ALL_PROXY", httpsProxy)
	}
	var no []string
	for _, e := range exceptions {
		e = strings.TrimPrefix(e, "*")
		if e != "" && !strings.Contains(e, "*") {
			no = append(no, e)
		}
	}
	set("NO_PROXY", strings.Join(no, ","))
	return v
}
