// Package settings keeps SSHYard's preferences in a JSON file.
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
)

// Settings are the user's preferences.
type Settings struct {
	Theme theme.Mode `json:"theme"`
	// Language is "" to follow the system, else "en" or "zh-Hans".
	Language         string  `json:"language"`
	CheckUpdates     bool    `json:"checkUpdates"`
	TerminalFontSize int     `json:"terminalFontSize"`
	KeepAliveSeconds int     `json:"keepAliveSeconds"`
	AutoReconnect    bool    `json:"autoReconnect"`
	SidebarWidth     float32 `json:"sidebarWidth"`
	// Privacy masks the IP addresses the app shows, for a screen share
	// or a recording. What is typed in a form is shown as it is.
	Privacy bool `json:"privacy,omitempty"`
	// ChromeMaterial is how the sidebar and the title bar show the
	// window's material (macOS): MaterialTranslucent, the default, or
	// MaterialOpaque. ContentMaterial is that of the content beside them,
	// opaque by default.
	ChromeMaterial  string `json:"chromeMaterial,omitempty"`
	ContentMaterial string `json:"contentMaterial,omitempty"`
	// UIFont is the interface's font family, "" for the system's.
	UIFont string `json:"uiFont,omitempty"`
	// TerminalFont is the terminal's font family, "" for the system's
	// monospaced font.
	TerminalFont string `json:"terminalFont,omitempty"`
	// MonospacedOnly limits the terminal font choice to monospaced
	// families.
	MonospacedOnly bool `json:"monospacedOnly"`
	// TransferConcurrency is how many SFTP transfers of a files pane run
	// at once, 1 to 10.
	TransferConcurrency int `json:"transferConcurrency"`
	// OpenConfirmMB is the size, in MB, from which opening a remote file
	// (in the editor or a local program) asks first, 1 to 1024.
	OpenConfirmMB int `json:"openConfirmMB"`
	// OSC52Write is whether a remote program may copy to the local
	// clipboard: allow (the default), ask or deny.
	OSC52Write string `json:"osc52Write"`
	// OSC52MaxBytes is the largest decoded OSC 52 paste to keep,
	// from 64 KiB to 8 MiB.
	OSC52MaxBytes int `json:"osc52MaxBytes"`
	// MetricsEnabled shows the terminal metrics bar. Off hides it on every
	// host. A host can still hide its own bar while this is on.
	MetricsEnabled bool `json:"metricsEnabled"`
	// MetricsInterval is how often a host is sampled, in seconds: 1, 2, 5 or 10.
	MetricsInterval int `json:"metricsInterval"`
	// MetricsMount is an extra filesystem shown beside the root. Empty is the root only.
	MetricsMount string `json:"metricsMount,omitempty"`
	// AIDefaultAgent is the agent a new AI pane starts with. "builtin"
	// is the in-process OpenAI-compatible agent.
	AIDefaultAgent string `json:"aiDefaultAgent,omitempty"`
	// AIDefaultMode is the global permission mode: ask, review or full.
	// "full" is kept when a file already says so; the UI asks before
	// writing it.
	AIDefaultMode string `json:"aiDefaultMode,omitempty"`
	// AIReviewProvider is an AIProviders ID, or empty for no review model.
	AIReviewProvider string `json:"aiReviewProvider,omitempty"`
	// AIContextLines is how many lines of the linked terminal are sent
	// with a question, 50 to 2000.
	AIContextLines int `json:"aiContextLines,omitempty"`
	// AIAuditRetentionDays is how long audit files are kept.
	AIAuditRetentionDays int `json:"aiAuditRetentionDays,omitempty"`
	// AIRedact masks suspected private keys and tokens before they are
	// sent to a model. The default is on; false is stored so it survives.
	AIRedact bool `json:"aiRedact"`
	// AIProviders are built-in agent profiles. API keys are not here;
	// they live in the keychain.
	AIProviders []AIProvider `json:"aiProviders,omitempty"`
	// AIConsentHosts are host IDs whose first-use AI notice the user
	// already confirmed. Nothing is sent for a host that is not listed.
	AIConsentHosts []string `json:"aiConsentHosts,omitempty"`
}

// AIProvider is one built-in agent profile. Headers are non-secret;
// the API key is keychain.AIKey(ID).
type AIProvider struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	BaseURL     string            `json:"baseUrl"`
	Model       string            `json:"model"`
	Headers     map[string]string `json:"headers,omitempty"`
	Temperature float64           `json:"temperature,omitempty"`
	MaxTokens   int               `json:"maxTokens,omitempty"`
}

const (
	// OSC52Allow lets a remote program copy to the clipboard.
	OSC52Allow = "allow"
	// OSC52Ask asks before each copy.
	OSC52Ask = "ask"
	// OSC52Deny ignores remote copies.
	OSC52Deny = "deny"
	// OSC52MaxDefault is 1 MiB. OSC52MaxMin and OSC52MaxMax bound the setting.
	OSC52MaxDefault = 1 << 20
	OSC52MaxMin     = 64 << 10
	OSC52MaxMax     = 8 << 20
)

// The materials of a part of the window: the system's translucent
// material behind it, or its own solid fill.
const (
	MaterialTranslucent = "translucent"
	MaterialOpaque      = "opaque"
)

func materialOK(s string) bool { return s == MaterialTranslucent || s == MaterialOpaque }

// Languages are the languages SSHYard ships, besides following the system.
var Languages = []string{"en", "zh-Hans"}

// Default returns the defaults.
func Default() Settings {
	return Settings{Theme: theme.Auto, CheckUpdates: true, TerminalFontSize: 13, KeepAliveSeconds: 30, AutoReconnect: true, SidebarWidth: 240, ChromeMaterial: MaterialTranslucent, ContentMaterial: MaterialOpaque, MonospacedOnly: true, TransferConcurrency: 3, OpenConfirmMB: 10, OSC52Write: OSC52Ask, OSC52MaxBytes: OSC52MaxDefault, MetricsEnabled: true, MetricsInterval: 2,
		AIDefaultAgent: "builtin", AIDefaultMode: "ask", AIContextLines: 200, AIAuditRetentionDays: 90, AIRedact: true}
}

// ResolveOSC52Write is the write policy in effect. A host value of allow,
// ask or deny wins; otherwise the global policy is used. Anything else
// is ask.
func ResolveOSC52Write(global, host string) string {
	switch host {
	case OSC52Allow, OSC52Ask, OSC52Deny:
		return host
	}
	switch global {
	case OSC52Allow, OSC52Ask, OSC52Deny:
		return global
	}
	return OSC52Ask
}

// Normalize replaces invalid values with defaults.
func (s *Settings) Normalize() {
	d := Default()
	if !s.Theme.Valid() {
		s.Theme = d.Theme
	}
	ok := s.Language == ""
	for _, l := range Languages {
		ok = ok || s.Language == l
	}
	if !ok {
		s.Language = ""
	}
	if s.TerminalFontSize < 8 || s.TerminalFontSize > 32 {
		s.TerminalFontSize = d.TerminalFontSize
	}
	if s.KeepAliveSeconds < 0 || s.KeepAliveSeconds > 600 {
		s.KeepAliveSeconds = d.KeepAliveSeconds
	}
	if !materialOK(s.ChromeMaterial) {
		s.ChromeMaterial = d.ChromeMaterial
	}
	if !materialOK(s.ContentMaterial) {
		s.ContentMaterial = d.ContentMaterial
	}
	s.UIFont, s.TerminalFont = fontName(s.UIFont), fontName(s.TerminalFont)
	if s.SidebarWidth < 160 || s.SidebarWidth > 480 {
		s.SidebarWidth = d.SidebarWidth
	}
	if s.TransferConcurrency < 1 || s.TransferConcurrency > 10 {
		s.TransferConcurrency = d.TransferConcurrency
	}
	if s.OpenConfirmMB < 1 || s.OpenConfirmMB > 1024 {
		s.OpenConfirmMB = d.OpenConfirmMB
	}
	switch s.OSC52Write {
	case OSC52Allow, OSC52Ask, OSC52Deny:
	default:
		s.OSC52Write = OSC52Ask
	}
	if s.OSC52MaxBytes < OSC52MaxMin || s.OSC52MaxBytes > OSC52MaxMax {
		s.OSC52MaxBytes = OSC52MaxDefault
	}
	s.MetricsInterval = session.MetricsInterval(s.MetricsInterval)
	if !session.MetricsMountOK(s.MetricsMount) {
		s.MetricsMount = ""
	}
	s.normalizeAI(d)
}

// aiModes are the permission modes a settings file may name.
var aiModes = map[string]bool{"ask": true, "review": true, "full": true}

func (s *Settings) normalizeAI(d Settings) {
	s.AIDefaultAgent = strings.TrimSpace(s.AIDefaultAgent)
	if s.AIDefaultAgent == "" {
		s.AIDefaultAgent = d.AIDefaultAgent
	}
	s.AIDefaultMode = strings.TrimSpace(s.AIDefaultMode)
	if !aiModes[s.AIDefaultMode] {
		s.AIDefaultMode = d.AIDefaultMode
	}
	switch {
	case s.AIContextLines <= 0:
		s.AIContextLines = d.AIContextLines
	case s.AIContextLines < 50:
		s.AIContextLines = 50
	case s.AIContextLines > 2000:
		s.AIContextLines = 2000
	}
	switch {
	case s.AIAuditRetentionDays <= 0:
		s.AIAuditRetentionDays = d.AIAuditRetentionDays
	case s.AIAuditRetentionDays > 3650:
		s.AIAuditRetentionDays = 3650
	}
	known := map[string]bool{}
	for i := range s.AIProviders {
		s.AIProviders[i].normalize()
		if id := s.AIProviders[i].ID; id != "" {
			known[id] = true
		}
	}
	s.AIReviewProvider = strings.TrimSpace(s.AIReviewProvider)
	if s.AIReviewProvider != "" && !known[s.AIReviewProvider] {
		s.AIReviewProvider = ""
	}
	s.AIConsentHosts = cleanList(s.AIConsentHosts)
}

func (p *AIProvider) normalize() {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	p.Model = strings.TrimSpace(p.Model)
	if p.Temperature < 0 || p.Temperature > 2 {
		p.Temperature = 0
	}
	if p.MaxTokens < 0 {
		p.MaxTokens = 0
	}
	if p.MaxTokens > 200000 {
		p.MaxTokens = 200000
	}
	p.Headers = cleanHeaders(p.Headers)
}

func cleanList(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// ErrBaseURL reports a base URL that is not http(s) with a host, or that
// carries user info (a secret does not belong in the settings).
var ErrBaseURL = errors.New("settings: base URL must be http or https with a host")

// ErrPlainHTTP warns that a base URL is plain HTTP to a host that is not
// this computer. CheckBaseURL still returns a nil error for it.
var ErrPlainHTTP = errors.New("settings: plain http to a non-loopback host")

// ErrHeader reports a header name or value SSHYard will not send, including
// Authorization (the API key lives in the keychain).
var ErrHeader = errors.New("settings: invalid header")

// CheckBaseURL checks a provider base URL. warn is ErrPlainHTTP for plain
// http to a host outside this computer; err rejects anything that is not
// an http(s) URL with a host and no user info.
func CheckBaseURL(raw string) (warn, err error) {
	u, perr := url.Parse(strings.TrimSpace(raw))
	if perr != nil || u.Host == "" || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, ErrBaseURL
	}
	if u.Scheme == "http" && !loopback(u.Hostname()) {
		return ErrPlainHTTP, nil
	}
	return nil, nil
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CheckHeaders rejects a CR, LF or NUL in a name or value, an empty name,
// and Authorization (any case). The API key is not a header.
func CheckHeaders(h map[string]string) error {
	for k, v := range h {
		if headerBad(k) || headerBad(v) || strings.EqualFold(strings.TrimSpace(k), "Authorization") {
			return ErrHeader
		}
	}
	return nil
}

func headerBad(s string) bool {
	return strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\r\n\x00")
}

// cleanHeaders drops headers CheckHeaders would reject. A provider loaded
// from disk must not be able to put the API key, or a split line, on the
// wire.
func cleanHeaders(h map[string]string) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range h {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if headerBad(k) || strings.ContainsAny(v, "\r\n\x00") || strings.EqualFold(k, "Authorization") {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// fontName cleans a font family name: one family, as a comma would make
// a fallback list of it, of a sane length.
func fontName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 128 || strings.ContainsAny(s, ",;\n\r\t\x00") {
		return ""
	}
	return s
}

// Load reads the settings at path; a missing file gives the defaults.
func Load(path string) (Settings, error) {
	s := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Default(), err
	}
	s.Normalize()
	return s, nil
}

// Save writes the settings to path atomically.
func Save(path string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, b, 0o600)
}

// WriteFileAtomic writes data to a temporary file beside path and renames
// it over path.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
