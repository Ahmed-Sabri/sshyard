// Package mcp serves tools to AI agents over the Model Context Protocol's
// Streamable HTTP transport, on this computer only. It answers JSON (no
// event stream and no sessions): initialize, ping, tools/list and
// tools/call. Every request needs the bearer token, and a Host or Origin
// that is not this computer is refused, against DNS rebinding.
package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Path is where the endpoint is served.
const Path = "/mcp"

// MaxBody is the largest request accepted.
const MaxBody = 1 << 20

// Versions are the protocol versions spoken, newest first. A client
// asking for another gets the newest.
var Versions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// Tool is one tool. Call's text is the result; an error is a failed
// result the agent reads, not a protocol error.
type Tool struct {
	Name        string
	Description string
	// Schema is the JSON Schema of the arguments.
	Schema json.RawMessage
	Call   func(ctx context.Context, args json.RawMessage) (string, error)
}

// Handler is the endpoint.
type Handler struct {
	Name, Version string
	// Instructions tell the agent how to use the tools.
	Instructions string
	// Token is the bearer token every request carries. Empty refuses all.
	Token string
	Tools []Tool
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type reply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// JSON-RPC error codes.
const (
	codeParse    = -32700
	codeInvalid  = -32600
	codeNoMethod = -32601
	codeParams   = -32602
)

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != Path {
		http.NotFound(w, r)
		return
	}
	if !loopbackHost(r.Host) || !originOK(r.Header.Get("Origin")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !h.authorized(r.Header.Get("Authorization")) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="sshyard"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); !strings.EqualFold(strings.TrimSpace(ct), "application/json") {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil || len(body) > MaxBody {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	var msg message
	if err := json.Unmarshal(body, &msg); err != nil {
		writeJSON(w, http.StatusBadRequest, reply{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "parse error"}})
		return
	}
	// A notification or a response to the server: nothing to answer.
	if len(msg.ID) == 0 || msg.Method == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	res, rerr := h.handle(r.Context(), msg)
	out := reply{JSONRPC: "2.0", ID: msg.ID, Result: res, Error: rerr}
	if rerr == nil && res == nil {
		out.Result = struct{}{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) authorized(header string) bool {
	scheme, tok, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || h.Token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(tok)), []byte(h.Token)) == 1
}

func (h *Handler) handle(ctx context.Context, msg message) (any, *rpcError) {
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		v := Versions[0]
		if slices.Contains(Versions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": h.Name, "version": h.Version},
			"instructions":    h.Instructions,
		}, nil
	case "ping":
		return nil, nil
	case "tools/list":
		tools := make([]map[string]any, 0, len(h.Tools))
		for _, t := range h.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil, &rpcError{codeParams, "invalid params"}
		}
		i := slices.IndexFunc(h.Tools, func(t Tool) bool { return t.Name == p.Name })
		if i < 0 {
			return nil, &rpcError{codeParams, "unknown tool: " + p.Name}
		}
		args := p.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage("{}")
		}
		text, err := h.Tools[i].Call(ctx, args)
		if err != nil {
			text = err.Error()
		}
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": err != nil,
		}, nil
	case "":
		return nil, &rpcError{codeInvalid, "invalid request"}
	}
	return nil, &rpcError{codeNoMethod, "method not found: " + msg.Method}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// loopbackHost says the Host header names this computer.
func loopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// originOK lets a request without an Origin (an agent, not a browser)
// through, and a browser's only from a page on this computer.
func originOK(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && loopbackHost(u.Host)
}

// Server is the endpoint listening on 127.0.0.1.
type Server struct {
	ln net.Listener
	hs *http.Server
}

// ErrBadPort reports a port outside 1–65535.
var ErrBadPort = errors.New("mcp: port must be 1 to 65535")

// Listen serves h on 127.0.0.1:port (0 picks one).
func Listen(port int, h http.Handler) (*Server, error) {
	if port < 0 || port > 65535 {
		return nil, ErrBadPort
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, hs: &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 64 << 10}}
	go s.hs.Serve(ln)
	return s, nil
}

// Port is the port it listens on.
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// URL is the endpoint's address.
func (s *Server) URL() string { return "http://127.0.0.1:" + strconv.Itoa(s.Port()) + Path }

// Close stops listening and drops every connection, which cancels the
// tools running for them.
func (s *Server) Close() error { return s.hs.Close() }
