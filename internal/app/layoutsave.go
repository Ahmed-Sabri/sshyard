package app

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/settings"
)

// layoutSnap is the workspace written to layout.json. Session content
// (terminal scrollback, editor buffer, AI timeline) is not saved.
// A missing or unreadable file starts empty; there is no migration.
type layoutSnap struct {
	Active int       `json:"active"`
	Tabs   []tabSnap `json:"tabs"`
}

type tabSnap struct {
	Focus int       `json:"focus"`
	Root  *nodeSnap `json:"root"`
}

type nodeSnap struct {
	Split    string    `json:"split,omitempty"`
	Ratio    float32   `json:"ratio,omitempty"`
	A        *nodeSnap `json:"a,omitempty"`
	B        *nodeSnap `json:"b,omitempty"`
	Kind     string    `json:"kind,omitempty"`
	Host     string    `json:"host,omitempty"`
	Path     string    `json:"path,omitempty"`
	Agent    string    `json:"agent,omitempty"`
	Mode     string    `json:"mode,omitempty"`
	Provider string    `json:"provider,omitempty"`
	Model    string    `json:"model,omitempty"`
	Effort   string    `json:"effort,omitempty"`
}

func (a *App) layoutPath() string {
	if a.p.DataDir == "" {
		return ""
	}
	return filepath.Join(a.p.DataDir, "layout.json")
}

func (a *App) saveLayout() {
	file := a.layoutPath()
	if file == "" || a.restoring || !a.unlocked() {
		return
	}
	snap := layoutSnap{}
	for i, t := range a.ws.Tabs {
		if t == nil || t.Root == nil {
			continue
		}
		n := 0
		focus := 0
		for _, p := range t.Panes() {
			if !layoutAI(a.conns[p.Item.ID]) {
				continue
			}
			n++
			if p.ID == t.Focus {
				focus = n
			}
		}
		root := a.snapNode(t.Root)
		if root == nil {
			continue
		}
		if i == a.ws.Active {
			snap.Active = len(snap.Tabs)
		}
		snap.Tabs = append(snap.Tabs, tabSnap{Focus: focus, Root: root})
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	_ = settings.WriteFileAtomic(file, append(b, '\n'), 0o600)
}

func (a *App) snapNode(n *layout.Node) *nodeSnap {
	if n == nil {
		return nil
	}
	if !n.Leaf() {
		left := a.snapNode(n.A)
		right := a.snapNode(n.B)
		if left == nil {
			return right
		}
		if right == nil {
			return left
		}
		split := "row"
		if n.Axis == layout.Column {
			split = "column"
		}
		return &nodeSnap{Split: split, Ratio: n.Ratio, A: left, B: right}
	}
	c := a.conns[n.Item.ID]
	if !layoutAI(c) {
		return nil
	}
	return &nodeSnap{Kind: "ai", Host: c.host.ID, Agent: c.ai.agent, Mode: c.ai.mode, Provider: c.ai.provider, Model: c.ai.model, Effort: c.ai.effort}
}

// layoutAI is a pane the layout file keeps: an AI pane on a saved host.
// Terminals, files and editors are not restored, so they are not saved.
func layoutAI(c *conn) bool {
	return c != nil && c.kind == layout.AI && c.ai != nil && !isTemp(c.host.ID)
}

func (a *App) loadLayout() {
	file := a.layoutPath()
	if file == "" || len(a.ws.Tabs) > 0 {
		return
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return // missing file is a fresh window; a bad read is left as-is
	}
	var snap layoutSnap
	if err := json.Unmarshal(b, &snap); err != nil {
		return // corrupt: start empty, keep the file
	}
	a.restoring = true
	defer func() { a.restoring = false }()
	for _, t := range snap.Tabs {
		ai := false // a tab has one AI pane; a second one saved is dropped
		root := a.restoreNode(t.Root, &ai)
		if root == nil {
			continue
		}
		a.ws.AddTab(root, 0)
		tab := a.ws.ActiveTab()
		panes := tab.Panes()
		if t.Focus >= 1 && t.Focus <= len(panes) {
			tab.Focus = panes[t.Focus-1].ID
		}
	}
	if snap.Active >= 0 && snap.Active < len(a.ws.Tabs) {
		a.ws.Active = snap.Active
	}
}

func (a *App) restoreNode(n *nodeSnap, ai *bool) *layout.Node {
	if n == nil {
		return nil
	}
	if n.Split == "row" || n.Split == "column" {
		left := a.restoreNode(n.A, ai)
		right := a.restoreNode(n.B, ai)
		if left == nil {
			return right
		}
		if right == nil {
			return left
		}
		axis := layout.Row
		if n.Split == "column" {
			axis = layout.Column
		}
		ratio := n.Ratio
		if ratio <= 0 || ratio >= 1 {
			ratio = 0.5
		}
		return &layout.Node{ID: a.ws.FreshID(), Axis: axis, Ratio: ratio, A: left, B: right}
	}
	if *ai {
		return nil
	}
	c := a.restoreLeaf(n)
	if c == nil {
		return nil
	}
	*ai = true
	return &layout.Node{ID: a.ws.FreshID(), Item: c.item()}
}

// restoreLeaf brings back an AI pane only. A terminal, files or editor
// leaf is dropped, so launch never dials and never reloads a buffer.
// The linked terminal is gone; the pane stays unlinked until the user picks one.
func (a *App) restoreLeaf(n *nodeSnap) *conn {
	if n.Kind != "ai" {
		return nil
	}
	c, err := a.makeConn(n.Host, layout.AI, 0)
	if err != nil {
		return nil
	}
	if n.Agent != "" {
		c.ai.agent = aiAgentName(n.Agent)
	}
	// "full" is this session only. A saved copy must not turn it back on.
	if n.Mode != "full" {
		c.ai.mode = n.Mode
	}
	c.ai.provider, c.ai.model, c.ai.effort = n.Provider, n.Model, n.Effort
	return c
}
