// Package app is SSHYard's interface: the host sidebar, the tabs and split
// panes (native terminals and files browsers alike) and the settings, built with
// MyGo's immediate-mode ui package. Everything platform-specific (the
// window's theme and language, the keychain, the updater, file dialogs)
// comes in through Platform, so the whole interface runs in ui.Tester.
package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/audit"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/forward"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/netproxy"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/updater"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// Platform is what the app needs from the system. Zero fields fall back
// to what works anywhere: in-memory data, an in-memory keychain, updates
// off.
type Platform struct {
	// OpenPath opens a file in its default app; nil can't.
	OpenPath func(path string) error
	// CacheDir is where files opened in another app are copied, the
	// user's cache folder when "".
	CacheDir string
	// DataDir holds hosts.json, settings.json and known_hosts; "" keeps
	// everything in memory.
	DataDir string
	Version string
	// Secrets is the system keychain; nil or unavailable falls back to
	// memory, for this run only, and the sidebar says so.
	Secrets keychain.Backend
	Updates updater.Backend
	// SetTheme and SetLocale apply the user's choice to the window.
	SetTheme  func(theme.Mode)
	SetLocale func(tag string)
	// PickFile shows the system's open dialog; it may block.
	PickFile func(title string) (string, error)
	// Dial replaces net.Dialer, for tests.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// HomeDir is where the local side of a files pane starts, and whose
	// .ssh/config the import reads; "" is the user's home.
	HomeDir string
	// LocalUser is the login name a host without one defaults to, as ssh
	// does; "" is the user running the app.
	LocalUser string
}

type page int

const (
	// pageWorkspace is the tabs and their panes.
	pageWorkspace page = iota
	pageSettings
)

// conn is what one pane shows, a terminal or a files browser, with its
// session: a shell, or for files no shell, on the host's connection that
// the panes on the host share (see session.Pool). Each pane still lives,
// reconnects and closes on its own.
type conn struct {
	id   int
	kind layout.Kind
	host assets.Host
	s    *session.Session
	tv   *termView   // terminal panes
	fs   *filesState // files and editor panes (its SFTP client)
	ed   *editState  // editor panes
	// handled is the failure a prompt was already shown for
	handled error
	// tried is, by a.typed key, the generation of the secret the last
	// attempt used (one per hop of a jump chain), so that a failure with
	// a secret since replaced does not ask again. Under App.mu.
	tried map[string]int
	// fw is the port forward the connection serves, shown in no pane
	fw *forward.Forward
	// cwd is the directory a terminal last reported, cwdSeq when (from
	// App.cwdSeq), and up since when it is connected
	cwd    string
	cwdSeq int
	up     time.Time
	// name is what the server says its hostname is, asked once per
	// connect (nameConn) when a report names a host; see cwdOnHost
	name     string
	nameAsk  nameState
	nameConn int
	// probe is how far reading the shell's directory from the server
	// went, for a terminal that does not report it, per connect
	// (probeConn), and probeAt when it was last read; see probeCwd
	probe     probeState
	probeConn int
	probeAt   time.Time
	// temp is the ID of the temporary host the pane was opened on, kept
	// after it is saved as a host, so that what was typed for it goes
	// when its last pane closes
	temp string
	// termNo is this pane's number among the host's terminal panes, in
	// the order they were opened, from 1. It stays until the pane
	// closes. A freed number is not reused while any other terminal pane
	// of that host is open; the count starts over when the last one
	// closes. Files panes are not numbered. 0 is not a terminal pane.
	termNo int
	// metCPU, metDisk, metNet and metGPU are this pane's metrics popovers.
	metCPU, metDisk, metNet, metGPU bool
	// ai is set for an AI pane. It has no session of its own.
	ai *aiState
}

func (c *conn) item() layout.Item { return layout.Item{Kind: c.kind, ID: c.id} }

// close ends the connection and what runs on it.
func (c *conn) close() {
	if c.fs != nil {
		c.fs.close()
	}
	if c.fw != nil {
		c.fw.Stop()
		return
	}
	if c.s != nil {
		c.s.Close()
	}
}

type sessTarget struct{ s *session.Session }

func (t sessTarget) Terminal() *vt.Terminal      { return t.s.Term }
func (t sessTarget) Write(p []byte) (int, error) { return t.s.Write(p) }
func (t sessTarget) Resize(cols, rows int)       { t.s.Resize(cols, rows) }

// App is the interface's state. Its View runs on the UI thread; work in
// the background posts its results with post.
type App struct {
	p          Platform
	settings   settings.Settings
	store      *assets.Store
	kc         *keychain.Keychain
	keychainOK bool
	known      *session.KnownHosts
	ws         *layout.Workspace
	conns      map[int]*conn
	// cwdSeq counts the directory reports of terminals
	cwdSeq   int
	nextConn int
	// pool shares one authenticated connection per host among its panes
	pool *session.Pool
	// typed are the secrets typed this run and never saved, by
	// promptKey: an "ask" host's password, a passphrase asked each run, a
	// password typed after the saved one was refused. Under mu:
	// credentials read them off the UI thread.
	typed map[string]string
	// typedGen counts the answers to the prompt for each key. Under mu.
	typedGen map[string]int
	upd      *updater.Service

	mu    sync.Mutex
	queue []func()
	svc   ui.Services
	live  bool

	// interface state
	page page
	// pref is the settings page showing, inspector the panel open beside
	// the panes, "" for none
	pref        prefPage
	inspector   inspectorKind
	selected    string // host chosen in the sidebar
	search      string
	searchFocus bool // the search field takes the focus next frame
	sections    map[string]*bool
	form        *hostForm
	editing     bool
	deleting    bool
	deleteID    string
	prompt      *credPrompt
	promptHold  focusHold
	editorHold  focusHold
	credHold    focusHold
	proxyHold   focusHold
	installHold focusHold
	// credForm is the credential sheet's fields; credDelete the
	// credential the delete dialog is for, and rebindChoice where its
	// hosts go
	credForm    *credForm
	credEditing bool
	// proxyForm is the proxy sheet open, from settings or the host editor
	proxyForm    *proxyForm
	proxyEditing bool
	credDelete   string
	credDeleting bool
	rebindChoice string
	install      *installForm
	installing   bool
	mkdirHold    focusHold
	prompting    bool
	hostKey      *hostKeyAsk
	askingKey    bool
	finding      bool
	query        string
	match        int
	focusReq     int // session whose terminal takes the focus next frame
	pct          map[int]*float32
	langChoice   string
	fontChoice   float64
	keepChoice   float64
	// toasts are shown at the next frame, said from outside one
	toasts []func(m locales.Messages) ui.Toast
	// the transfers at once and the size to confirm opening, as edited
	concChoice, openMBChoice float64
	osc52Choice              int
	osc52KiB                 float64
	clip                     clipGate
	clipAsk                  *oscAsk
	sbWidth                  float32
	updateCheck              bool
	themeIdx                 int
	autoRecon                bool
	fonts                    fontState
	// paneBounds are where the panes were last frame, by session
	paneBounds map[int]ui.Rect
	// dropPreview is where a drag in progress would land, nil for none
	dropPreview *dropTarget
	// temps are the temporary hosts of quick connect, by ID, kept while a
	// pane uses them and never saved; nextTemp numbers them
	temps     map[string]assets.Host
	nextTemp  int
	quick     *quickForm
	quicking  bool
	quickHold focusHold
	imp       *importForm
	importing bool
	impHold   focusHold
	// fwds are the port forwards running, by forward ID
	fwds       map[string]*fwdRun
	fwdForm    *fwdForm
	fwdEditing bool
	fwdHold    focusHold
	// fwdDeleting asks before deleting the forward fwdDeleteID.
	fwdDeleting bool
	fwdDeleteID string
	// fwdOpen are the forwards whose connections the sidebar lists
	fwdOpen map[string]bool
	// termSeq is the next terminal-pane number per host. See conn.termNo.
	termSeq map[string]int
	// metrics are the host collectors, by the host's pool key. One per
	// host, shared by its terminal panes. The channel is not a pool
	// reference. metMu guards the map and minimized; sessions stay on
	// the UI thread.
	metrics   map[string]*session.Collector
	metMu     sync.Mutex
	minimized bool
	// metOn, metEvery and metMount are the settings controls.
	// metMountFocus is set while the mount field has the keyboard, so
	// leaving it or the settings page can save what was typed.
	metOn         bool
	metEvery      int
	metMount      string
	metMountFocus bool
	// newGate, newTools and newModel are test seams. Nil uses
	// policy.NewGate, the host's remote tools, and the OpenAI client.
	newGate  func(mode func() policy.Mode, ask policy.Prompter) policy.Gate
	newTools func(*conn) agent.Tools
	newModel func(*conn) agent.Streamer
	// audit is one log for every pane, so concurrent writes share its lock.
	audit *audit.Log
	// aiRecent is the AI pane last used on a host, by host ID.
	aiRecent  map[string]int
	aiLines   float64
	aiDays    float64
	aiModeIdx int
	aiHold    focusHold
	fullHold  focusHold
	full      *fullAsk
	consent   *consentAsk
	prov      *providerForm
	provOpen  bool
	// provDelete asks before removing a provider and its keychain key.
	provDeleting bool
	provDeleteID string
	askOpen      bool
	askFor       int
	// closing is set while Close saves the layout, so each pane does not.
	closing bool
	// restoring is set while a saved layout is opened, so it is not saved
	// again over itself mid-way.
	restoring bool
	// localName is what local terminals are called, in the language of
	// the last frame.
	localName string
	// group is the group sheet open (new or rename), groupDeleting asks
	// before deleting groupDelete
	group         *groupForm
	grouping      bool
	groupHold     focusHold
	groupDeleting bool
	groupDelete   string
}

// New opens the app's data.
func New(p Platform) (*App, error) {
	a := &App{p: p, ws: layout.New(), conns: map[int]*conn{}, pool: session.NewPool(), typed: map[string]string{}, typedGen: map[string]int{}, sections: map[string]*bool{}, temps: map[string]assets.Host{}, pct: map[int]*float32{}, fwds: map[string]*fwdRun{}, fwdOpen: map[string]bool{}, paneBounds: map[int]ui.Rect{}, metrics: map[string]*session.Collector{}, aiRecent: map[string]int{}}
	a.settings = settings.Default()
	var errs []error
	path := func(name string) string {
		if p.DataDir == "" {
			return ""
		}
		return filepath.Join(p.DataDir, name)
	}
	if p.DataDir != "" {
		if err := os.MkdirAll(p.DataDir, 0o700); err != nil {
			errs = append(errs, err)
		}
		if s, err := settings.Load(path("settings.json")); err == nil {
			a.settings = s
		} else {
			errs = append(errs, err)
		}
	}
	st, err := assets.Open(path("hosts.json"))
	if err != nil {
		errs = append(errs, err)
		st, _ = assets.Open("")
	}
	a.store = st
	if kh := path("known_hosts"); kh != "" {
		a.known, err = session.OpenKnownHosts(kh)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if a.known == nil {
		f, _ := os.CreateTemp("", "sshyard-known_hosts-*")
		f.Close()
		a.known, _ = session.OpenKnownHosts(f.Name())
	}
	if p.Secrets != nil {
		a.kc = keychain.New(p.Secrets)
		a.keychainOK = a.kc.Available()
	}
	if !a.keychainOK {
		a.kc = keychain.New(keychain.NewMemory())
	}
	// The hosts of an older data file are gone (no migration); so are
	// the secrets the old version kept for them.
	for _, id := range a.store.Discarded() {
		for _, k := range keychain.Kinds {
			a.kc.Delete("host/" + id + "/" + string(k))
		}
	}
	backend := p.Updates
	if backend == nil {
		backend = disabledUpdates{}
	}
	a.upd = updater.New(backend, a.invalidate)
	a.syncChoices()
	a.resolveFonts(false, false)
	a.updateCheck = a.settings.CheckUpdates
	if a.settings.CheckUpdates && p.Updates != nil {
		go a.upd.Check(context.Background())
	}
	return a, errors.Join(errs...)
}

type disabledUpdates struct{}

func (disabledUpdates) Enabled() bool { return false }
func (disabledUpdates) Check(context.Context) (updater.Release, error) {
	return nil, updater.ErrDisabled
}
func (disabledUpdates) Relaunch() {}

func (a *App) syncChoices() {
	a.langChoice = a.settings.Language
	a.fontChoice = float64(a.settings.TerminalFontSize)
	a.keepChoice = float64(a.settings.KeepAliveSeconds)
	a.concChoice = float64(a.settings.TransferConcurrency)
	a.openMBChoice = float64(a.settings.OpenConfirmMB)
	a.osc52KiB = float64(a.settings.OSC52MaxBytes >> 10)
	a.sbWidth = float32(a.settings.SidebarWidth)
	a.metOn = a.settings.MetricsEnabled
	a.metEvery = intervalIndex(a.settings.MetricsInterval)
	a.metMount = a.settings.MetricsMount
	a.aiLines = float64(a.settings.AIContextLines)
	a.aiDays = float64(a.settings.AIAuditRetentionDays)
}

// Settings returns the settings in effect.
func (a *App) Settings() settings.Settings { return a.settings }

// Store returns the host store.
func (a *App) Store() *assets.Store { return a.store }

// Keychain returns the keychain in use, and whether it is the system's.
func (a *App) Keychain() (*keychain.Keychain, bool) { return a.kc, a.keychainOK }

// ApplySettings applies and saves settings.
func (a *App) ApplySettings(s settings.Settings) error {
	s.Normalize()
	old := a.settings
	a.settings = s
	if s.Theme != old.Theme && a.p.SetTheme != nil {
		a.p.SetTheme(s.Theme)
	}
	if s.Language != old.Language && a.p.SetLocale != nil {
		a.p.SetLocale(s.Language)
	}
	if s.TerminalFontSize != old.TerminalFontSize {
		for _, c := range a.conns {
			if c.tv != nil {
				c.tv.fontSize = float32(s.TerminalFontSize)
			}
		}
	}
	if s.TransferConcurrency != old.TransferConcurrency {
		for _, c := range a.conns {
			if c.fs != nil {
				c.fs.transfers.SetConcurrency(s.TransferConcurrency)
			}
		}
	}
	if s.OSC52MaxBytes != old.OSC52MaxBytes {
		for _, c := range a.conns {
			if c.tv != nil && c.s != nil && c.s.Term != nil {
				c.s.Term.SetOSC52Max(s.OSC52MaxBytes)
			}
		}
	}
	if uiF, termF := s.UIFont != old.UIFont, s.TerminalFont != old.TerminalFont; uiF || termF {
		a.resolveFonts(uiF, termF)
	}
	a.syncChoices()
	if a.p.DataDir == "" {
		return nil
	}
	return settings.Save(filepath.Join(a.p.DataDir, "settings.json"), s)
}

// Start applies the saved theme and language to the window.
func (a *App) Start() {
	a.loadFonts()
	if a.p.SetTheme != nil {
		a.p.SetTheme(a.settings.Theme)
	}
	if a.p.SetLocale != nil && a.settings.Language != "" {
		a.p.SetLocale(a.settings.Language)
	}
	a.autoStartForwards()
	a.loadLayout()
	if root, err := a.cacheDir(); err == nil {
		go cleanCache(root)
	}
}

// post runs fn on the UI thread before the next frame.
func (a *App) post(fn func()) {
	a.mu.Lock()
	a.queue = append(a.queue, fn)
	a.mu.Unlock()
	a.invalidate()
}

func (a *App) invalidate() {
	a.mu.Lock()
	svc, live := a.svc, a.live
	a.mu.Unlock()
	if live {
		svc.Invalidate()
	}
}

func (a *App) drain() {
	a.mu.Lock()
	q := a.queue
	a.queue = nil
	a.mu.Unlock()
	for _, fn := range q {
		fn()
	}
}

// Close ends every session and port forward.
func (a *App) Close() {
	a.stopCollectors()
	if !a.closing {
		a.closing = true
		a.saveLayout()
	}
	for id, c := range a.conns {
		a.clip.forget(id)
		if c.kind == layout.AI {
			a.StopAI(c.id)
		}
		c.close()
	}
	a.clipAsk = nil
	a.fwds = map[string]*fwdRun{}
}

// Open where a new session goes.
type openAt struct {
	pane int // 0 for a new tab
	edge layout.Edge
}

// Connect opens a terminal to a saved host, in a new tab or on a pane.
func (a *App) Connect(hostID string, at openAt) (int, error) {
	return a.open(hostID, layout.Terminal, at, nil)
}

// OpenFiles opens a files pane on a saved host, in a new tab or on a
// pane. from, when set, is the pane it was opened from (its host's
// connection, and secrets typed for it, serve every pane on the host).
func (a *App) OpenFiles(hostID string, at openAt, from *conn) (int, error) {
	return a.open(hostID, layout.Files, at, from)
}

func (a *App) open(hostID string, kind layout.Kind, at openAt, from *conn) (int, error) {
	c, err := a.makeConn(hostID, kind, 0)
	if err != nil {
		return 0, err
	}
	a.place(c, at)
	return c.id, nil
}

// makeConn builds a pane and registers it. It does not place or start it.
// An AI pane gets no SSH session.
func (a *App) makeConn(hostID string, kind layout.Kind, link int) (*conn, error) {
	h, ok := a.host(hostID)
	if !ok {
		return nil, assets.ErrNotFound
	}
	if isLocal(h.ID) && kind != layout.Terminal {
		return nil, errLocalTerminalOnly
	}
	a.nextConn++
	c := &conn{id: a.nextConn, kind: kind, host: h}
	if isTemp(h.ID) {
		c.temp = h.ID
	}
	if kind == layout.AI {
		agentName := a.settings.AIDefaultAgent
		if agentName == "" {
			agentName = "builtin"
		}
		c.ai = &aiState{agent: agentName, link: link}
		if len(a.settings.AIProviders) == 1 {
			c.ai.provider = a.settings.AIProviders[0].ID
		}
		a.conns[c.id] = c
		return c, nil
	}
	var cfg session.Config
	if !isLocal(h.ID) {
		cfg = a.hostConfig(h, c, func(ctx context.Context) (session.Credentials, error) { return a.credentials(c, h) })
	}
	opts := session.Options{
		KeepAlive:     time.Duration(a.settings.KeepAliveSeconds) * time.Second,
		AutoReconnect: a.settings.AutoReconnect,
		NoShell:       kind != layout.Terminal,
		Local:         isLocal(h.ID),
	}
	c.s = session.New(cfg, opts, 80, 24)
	c.s.OnChange = a.invalidate
	if kind != layout.Terminal {
		c.fs = newFilesState(a.invalidate)
		c.fs.transfers.Client = a.transferClient(c)
		c.fs.transfers.SetConcurrency(a.settings.TransferConcurrency)
		if kind == layout.Editor {
			c.ed = newEditState("")
		}
	} else {
		c.tv = newTermView(sessTarget{c.s}, float32(a.settings.TerminalFontSize))
		c.tv.family = a.termFamily()
		a.armOSC52(c)
		if a.termSeq == nil {
			a.termSeq = map[string]int{}
		}
		a.termSeq[h.ID]++
		c.termNo = a.termSeq[h.ID]
	}
	a.conns[c.id] = c
	return c, nil
}

// place shows c in the workspace and starts its session, when it has one.
func (a *App) place(c *conn, at openAt) {
	if at.pane == 0 || !a.ws.Place(c.item(), at.pane, at.edge) {
		a.ws.Open(c.item())
	}
	a.focusReq = c.id
	a.page = pageWorkspace
	if c.temp == "" {
		a.store.Touch(c.host.ID)
	}
	if c.s != nil {
		c.s.Start()
	}
	if !a.restoring {
		a.saveLayout()
	}
}

// hostConfig is how to reach h, on its shared connection, logging in
// with creds: through its jump hosts, each logging in with its own
// credentials (typed ones held for c), and the proxy of the first hop.
func (a *App) hostConfig(h assets.Host, c *conn, creds func(context.Context) (session.Credentials, error)) session.Config {
	cfg := session.Config{
		Pool:        a.pool,
		Key:         a.poolKey(h),
		Name:        h.Label(),
		ID:          h.ID,
		Addr:        h.Addr(),
		User:        h.User,
		HostKey:     a.known.Callback(),
		Dial:        a.p.Dial,
		Credentials: creds,
	}
	path, err := a.store.JumpPath(h.ID)
	if err != nil && !errors.Is(err, assets.ErrNotFound) {
		cfg.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, err }
		return cfg
	}
	for _, j := range path {
		cfg.Jumps = append(cfg.Jumps, session.Hop{
			Name: j.Label(), ID: j.ID, Addr: j.Addr(), User: j.User, HostKey: a.known.Callback(),
			Credentials: func(context.Context) (session.Credentials, error) { return a.credentials(c, j) },
		})
	}
	// The proxy is the host's; with none, the first jump host's own, as
	// that is how that host is reached.
	proxyID := h.ProxyID
	if proxyID == "" && len(path) > 0 {
		proxyID = path[0].ProxyID
	}
	if px, ok := a.store.Proxy(proxyID); ok {
		cfg.Proxy = a.proxyDialer(px)
	}
	return cfg
}

// proxyDialer dials through px, its password read from the keychain
// when it is needed.
func (a *App) proxyDialer(px assets.Proxy) *netproxy.Dialer {
	kind := netproxy.SOCKS5
	if px.Kind == assets.ProxyHTTP {
		kind = netproxy.HTTP
	}
	return &netproxy.Dialer{
		Kind: kind, Addr: px.Addr(), User: px.User, Forward: a.p.Dial,
		Password: func(context.Context) (string, error) {
			b, err := a.kc.Get(keychain.ProxyPassword(px.ID))
			if errors.Is(err, keychain.ErrNotFound) {
				return "", nil
			}
			return string(b), err
		},
	}
}

// poolKey is the identity of a host's shared connection: the host and
// every jump host before it, each with who and where it logs in as, how,
// and through which proxy, so that editing any of them connects anew.
func (a *App) poolKey(h assets.Host) string {
	key := a.hopKey(h)
	if len(h.JumpChain) > 0 {
		path, err := a.store.JumpPath(h.ID)
		if err != nil || h.ID == "" {
			// Not saved, or a bad chain: the IDs, as the dial will fail.
			return key + "|" + strings.Join(h.JumpChain, ",")
		}
		for _, j := range path {
			key += "|" + a.hopKey(j)
		}
	}
	return key
}

// hopKey is one hop of poolKey.
func (a *App) hopKey(h assets.Host) string {
	key := h.ID + "|" + h.User + "@" + h.Addr() + "|" + string(h.Auth) + "|" + h.CredentialID
	if cr, ok := a.store.Credential(h.CredentialID); ok && h.Auth == assets.AuthCredential {
		key += ":" + cr.User + ":" + string(cr.Kind)
	}
	key += "|" + h.ProxyID
	if p, ok := a.store.Proxy(h.ProxyID); ok {
		key += ":" + string(p.Kind) + ":" + p.User + "@" + net.JoinHostPort(p.Address, strconv.Itoa(p.Port))
	}
	return key
}

// promptKey is where the secret the user types for h goes in a.typed,
// and whether it is a passphrase; "" when nothing typed would help (an
// agent, a key that was refused or is missing).
func (a *App) promptKey(h assets.Host, kind session.Kind) (key string, passphrase bool) {
	switch h.Auth {
	case assets.AuthAsk:
		return "ask/" + h.ID, false
	case assets.AuthCredential:
		cr, ok := a.store.Credential(h.CredentialID)
		if !ok {
			return "", false
		}
		if cr.Kind == assets.CredPassword {
			return keychain.Cred(cr.ID, keychain.Password), false
		}
		if kind == session.KindPassphrase {
			return keychain.Cred(cr.ID, keychain.Passphrase), true
		}
	}
	return "", false
}

// credentials, h's for the connection c (c's host or one of its jump
// hosts), are read off the UI thread: what the user typed this run, then
// the keychain. An agent's keys are never read.
func (a *App) credentials(c *conn, h assets.Host) (session.Credentials, error) {
	typed := func(key string) string {
		a.mu.Lock()
		defer a.mu.Unlock()
		if c.tried == nil {
			c.tried = map[string]int{}
		}
		c.tried[key] = a.typedGen[key]
		return a.typed[key]
	}
	var out session.Credentials
	switch h.Auth {
	case assets.AuthAgent:
		out.Agent = true
	case assets.AuthCredential:
		cr, ok := a.store.Credential(h.CredentialID)
		if !ok {
			return out, session.ErrNoCredentials
		}
		out.User = cr.User
		switch cr.Kind {
		case assets.CredPassword:
			key := keychain.Cred(cr.ID, keychain.Password)
			out.Password = typed(key)
			if out.Password == "" {
				b, err := a.kc.Get(key)
				if err != nil {
					return out, session.ErrNoCredentials
				}
				out.Password = string(b)
			}
		case assets.CredKey:
			key, err := a.kc.Get(keychain.Cred(cr.ID, keychain.PrivateKey))
			if err != nil {
				return out, session.ErrNoCredentials
			}
			out.PrivateKey = key
			pk := keychain.Cred(cr.ID, keychain.Passphrase)
			if p := typed(pk); p != "" {
				out.Passphrase = []byte(p)
			} else if !cr.AskPassphrase {
				out.Passphrase, _ = a.kc.Get(pk)
			}
		}
	default:
		out.Password = typed("ask/" + h.ID)
		if out.Password == "" {
			return out, session.ErrNoCredentials
		}
	}
	return out, nil
}

// closeSoon closes the pane on the next frame. Closing while the frame
// is still walking the split tree overwrites the parent it is walking.
func (a *App) closeSoon(id int) {
	a.post(func() { a.CloseSession(id) })
}

// CloseSession closes a pane, terminal or files, and its connection.
func (a *App) CloseSession(id int) {
	c := a.conns[id]
	if c == nil {
		return
	}
	a.clip.forget(id)
	if a.clipAsk != nil && a.clipAsk.pane == id {
		a.clipAsk = nil
	}
	if c.kind == layout.AI {
		a.StopAI(id)
	}
	a.ws.Close(id)
	delete(a.conns, id)
	a.releaseTermNo(c)
	a.syncMetrics()
	// Copies go here, not in the goroutine. RemoveAll unlinks the file
	// before the folder, and c.close waits on the session.
	if c.fs != nil {
		c.fs.edits.close()
	}
	go c.close()
	a.dropTemp(c.temp)
	if !a.closing {
		a.saveLayout()
	}
}

// releaseTermNo forgets a host's pane numbers once its last terminal
// pane has closed, so the next one is 1 again.
func (a *App) releaseTermNo(c *conn) {
	if c.termNo == 0 {
		return
	}
	for _, o := range a.conns {
		if o.termNo > 0 && o.host.ID == c.host.ID {
			return
		}
	}
	delete(a.termSeq, c.host.ID)
}

// focused is what the active tab's focused pane shows.
func (a *App) focused() *conn {
	if t := a.ws.ActiveTab(); t != nil {
		return a.conns[t.Focused().ID]
	}
	return nil
}

// hostsBy groups the hosts the sidebar shows, filtered by the search.
func hostsBy(st *assets.Store, query string) (favorites []assets.Host, groups []string, byGroup map[string][]assets.Host) {
	hosts := st.List()
	if query != "" {
		hosts = st.Search(query)
	}
	byGroup = map[string][]assets.Host{}
	for _, h := range hosts {
		if h.Favorite {
			favorites = append(favorites, h)
		}
		if _, ok := byGroup[h.Group]; !ok && h.Group != "" {
			groups = append(groups, h.Group)
		}
		byGroup[h.Group] = append(byGroup[h.Group], h)
	}
	// Groups made on their own show while empty, but not in a search.
	if query == "" {
		for _, g := range st.Groups() {
			if _, ok := byGroup[g]; !ok {
				groups = append(groups, g)
				byGroup[g] = nil
			}
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	return
}
