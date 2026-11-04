package app

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/ai/history"
	"github.com/Licoy/sshyard/internal/aibrands"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// A saved host keeps its AI conversations (internal/ai/history), sealed by
// the keychain: a pane saves its own as each turn ends, as it starts
// another and as it closes, and can open one again from its history to
// carry on.

// chatHistory is the store of the conversations, nil while the keychain
// is locked.
func (a *App) chatHistory() *history.Store {
	if a.p.DataDir == "" || !a.unlocked() || a.kc == nil {
		return nil
	}
	return history.New(filepath.Join(a.p.DataDir, "ai-history"), a.kc)
}

// chats are host's saved conversations, the latest first, as last read.
func (a *App) chats(host string) []history.Summary {
	if list, ok := a.chatList[host]; ok {
		return list
	}
	st := a.chatHistory()
	if st == nil {
		return nil
	}
	list, _ := st.List(host)
	if a.chatList == nil {
		a.chatList = map[string][]history.Summary{}
	}
	a.chatList[host] = list
	return list
}

// keepsChats is whether the pane's conversations are saved: those on a
// saved host.
func (a *App) keepsChats(c *conn) bool {
	if c == nil || c.ai == nil || isTemp(c.host.ID) {
		return false
	}
	_, ok := a.store.Get(c.host.ID)
	return ok
}

// noteQuestion starts the pane's conversation with its first question:
// its ID and its title.
func (a *App) noteQuestion(c *conn, text string, files []*aiAttach) {
	if c.ai.conv == "" {
		c.ai.conv, c.ai.created = history.NewID(), time.Now()
	}
	if c.ai.title == "" {
		c.ai.title = history.Title(text)
		if c.ai.title == "" && len(files) > 0 {
			c.ai.title = files[0].name
		}
	}
}

// saveChat keeps the pane's conversation, once it has one.
func (a *App) saveChat(c *conn) {
	if !a.keepsChats(c) || c.ai.conv == "" || len(c.ai.items) == 0 {
		return
	}
	st := a.chatHistory()
	if st == nil {
		return
	}
	conv := history.Conversation{ID: c.ai.conv, Host: c.host.ID, Title: c.ai.title, Agent: c.ai.agent, Provider: c.ai.provider,
		Model: c.ai.model, Effort: c.ai.effort, Created: c.ai.created, Updated: time.Now(), Messages: c.ai.msgs}
	if c.ai.agent == "builtin" && c.ai.provider != "" {
		if p := a.paneProvider(c); p != nil && conv.Model == "" {
			conv.Model = p.Model
		}
	}
	for _, it := range c.ai.items {
		item := history.Item{Kind: it.kind, Text: it.text}
		for _, f := range it.files {
			item.Files = append(item.Files, history.File{Name: f.name, MIME: f.mime})
		}
		if it.kind == "tool" {
			card := it.tool
			item.Tool = &card
		}
		conv.Items = append(conv.Items, item)
	}
	delete(a.chatList, c.host.ID)
	if err := st.Save(conv); err != nil && !a.closing {
		a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
			return ui.Toast{Title: m.AiHistorySaveFailed(), Type: "error"}
		})
	}
}

// resetChat empties the pane for a new conversation, the current one
// left as it was saved.
func (a *App) resetChat(c *conn) {
	a.invalidateAI(c)
	if c.ai.acp != nil {
		c.ai.acp.close()
		c.ai.acp = nil
	}
	c.ai.turn++
	c.ai.running, c.ai.cancel, c.ai.paused = false, nil, false
	c.ai.sess, c.ai.box, c.ai.tools, c.ai.msgs = nil, nil, nil, nil
	c.ai.items, c.ai.input, c.ai.attach, c.ai.task = nil, "", nil, ""
	c.ai.conv, c.ai.title, c.ai.created, c.ai.forgot = "", "", time.Time{}, 0
	c.ai.gen++
	c.ai.scroll = ui.ScrollState{}
	a.focusReq = c.id
}

// NewChat saves the pane's conversation and starts another.
func (a *App) NewChat(id int) {
	c := a.conns[id]
	if c == nil || c.ai == nil || c.ai.running {
		return
	}
	a.saveChat(c)
	a.resetChat(c)
}

// ClearChat forgets the pane's conversation, from the history too, and
// starts another.
func (a *App) ClearChat(id int) {
	c := a.conns[id]
	if c == nil || c.ai == nil || c.ai.running {
		return
	}
	if c.ai.conv != "" {
		a.forgetChat(c.host.ID, c.ai.conv)
	}
	a.resetChat(c)
}

// forgetChat deletes host's conversation id from the history.
func (a *App) forgetChat(host, id string) {
	if st := a.chatHistory(); st != nil {
		_ = st.Delete(host, id)
	}
	delete(a.chatList, host)
}

// DeleteChat deletes host's conversation id; a pane showing it starts
// another.
func (a *App) DeleteChat(host, id string) {
	for _, c := range a.conns {
		if c.ai != nil && c.host.ID == host && c.ai.conv == id {
			if c.ai.running {
				return
			}
			a.resetChat(c)
		}
	}
	a.forgetChat(host, id)
}

// OpenChat shows the pane's host's conversation id in the pane, to carry
// on, after saving the one it showed. The built-in agent takes it up
// where it was; an external agent keeps its own memory, so it starts
// afresh after it (forgot).
func (a *App) OpenChat(paneID int, id string) {
	c := a.conns[paneID]
	if c == nil || c.ai == nil || id == c.ai.conv {
		return
	}
	if c.ai.running {
		a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast { return ui.Toast{Title: m.AiHistoryBusy()} })
		return
	}
	st := a.chatHistory()
	if st == nil {
		return
	}
	conv, err := st.Load(c.host.ID, id)
	if err != nil {
		if errors.Is(err, history.ErrNotFound) {
			delete(a.chatList, c.host.ID)
		}
		a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
			return ui.Toast{Title: m.AiHistoryLoadFailed(), Type: "error"}
		})
		return
	}
	a.saveChat(c)
	a.resetChat(c)
	if conv.Agent == "builtin" || conv.Agent == "acp" {
		c.ai.agent = conv.Agent
	}
	if a.providerByID(conv.Provider) != nil {
		c.ai.provider, c.ai.model, c.ai.effort = conv.Provider, conv.Model, conv.Effort
	}
	for _, it := range conv.Items {
		item := aiItem{kind: it.Kind, text: it.Text}
		for _, f := range it.Files {
			item.files = append(item.files, &aiAttach{name: f.Name, mime: f.MIME})
		}
		if it.Tool != nil {
			item.tool = *it.Tool
		}
		c.ai.items = append(c.ai.items, item)
	}
	c.ai.conv, c.ai.title, c.ai.created, c.ai.msgs = conv.ID, conv.Title, conv.Created, conv.Messages
	if c.ai.agent != "builtin" || len(c.ai.msgs) == 0 {
		c.ai.forgot = len(c.ai.items)
	}
	c.ai.scroll.Y = 1e9
	a.saveLayout()
}

// chatWhen is when a conversation was last carried on: the time today,
// the day this year, else the date.
func chatWhen(t, now time.Time) string {
	t = t.Local()
	switch {
	case t.YearDay() == now.YearDay() && t.Year() == now.Year():
		return t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("01-02 15:04")
	}
	return t.Format("2006-01-02")
}

// chatLead is the mark of a conversation in a list: its model's brand,
// else the robot.
func (a *App) chatLead(c *ui.Context, tok theme.Tokens, s history.Summary) func() {
	return func() {
		if p := a.providerByID(s.Provider); p != nil && s.Agent != "acp" {
			brandImage(c, aibrands.Model(s.Model, *p), 16)
			return
		}
		ui.Icon(c, icons.Robot.SVG).Size(16, 16).TextColor(tok.TextMuted)
	}
}

// chatTitle is how a list names a conversation.
func chatTitle(m locales.Messages, s history.Summary) string {
	if s.Title == "" {
		return m.AiUntitled()
	}
	return s.Title
}

// historyButton opens the pane's host's saved conversations: a search
// field, then each with its model and when, the one shown checked, and a
// button to delete it.
func (a *App) historyButton(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	key := "aihist:" + strconv.Itoa(cn.id)
	b := IconButton{Icon: icons.History, Label: m.AiChatHistory(), Size: Tiny, Disabled: cn.ai.running, Active: a.menu.key == key}
	el := b.Show(c, key).Role(ui.RoleMenuButton).Expanded(a.menu.key == key)
	open := a.menu.key == key
	if el.Clicked() && !b.Disabled {
		if open {
			a.menu = dropMenu{}
		} else {
			a.menu = dropMenu{key: key, sub: -1, focus: 0}
			cn.ai.hquery = ""
		}
		open = !open
	}
	if !open {
		return
	}
	ui.PopoverBase(c.Key("menu:"+key), el, &open, func(panel ui.Element) {
		panel.Margin(4, 0, 0, 0).Role(ui.RoleMenu).Width(300).Padding(5).Radius(theme.RadiusCard).Background(tok.Inset).
			Border(1, tok.GlassEdge).FocusGroup(ui.Vertical)
		tok.Lift.On(panel)
		panel.Children(func() {
			all := a.chats(cn.host.ID)
			if len(all) == 0 {
				ui.Text(c, m.AiHistoryEmpty()).FontSize(theme.Footnote).TextColor(tok.TextMuted).Padding(6, 8)
				return
			}
			in := ui.TextInput(c.Key("q"), &cn.ai.hquery).Placeholder(m.AiSearchChats()).Label(m.AiSearchChats()).
				FontSize(theme.Footnote).FillWidth().Margin(0, 0, 4, 0)
			if a.menu.focus == 0 {
				in.Focus()
				a.menu.focus = -1
			}
			q := strings.ToLower(strings.TrimSpace(cn.ai.hquery))
			var shown []history.Summary
			for _, s := range all {
				if q == "" || strings.Contains(strings.ToLower(chatTitle(m, s)), q) {
					shown = append(shown, s)
				}
			}
			if in.Submitted() && len(shown) > 0 {
				a.menu = dropMenu{}
				a.OpenChat(cn.id, shown[0].ID)
				return
			}
			ui.Scroll(c.Key("scroll")).MaxHeight(menuHeight).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					now := time.Now()
					for i, s := range shown {
						ui.Row(c.Key("h:" + s.ID)).FillWidth().AlignItems(ui.Center).Gap(2).Children(func() {
							e := menuEntry{Lead: a.chatLead(c, tok, s), Label: chatTitle(m, s), Note: chatWhen(s.Updated, now), Checked: s.ID == cn.ai.conv,
								Do: func() { a.OpenChat(cn.id, s.ID) }}
							a.menuRow(c, tok, e, i, 0).Grow(1).Shrink(1).MinWidth(0)
							if (IconButton{Icon: icons.Delete, Label: m.AiDeleteChat(), Size: Small, Danger: true}).Clicked(c, "del") {
								cn.ai.drop = s.ID
								a.menu = dropMenu{}
							}
						})
					}
					if len(shown) == 0 {
						ui.Text(c, m.AiNoChats()).FontSize(theme.Footnote).TextColor(tok.TextMuted).Padding(6, 8)
					}
				})
			})
		})
	})
	if !open && a.menu.key == key {
		a.menu = dropMenu{}
	}
}

// chatConfirm asks before the pane's conversation, or one of its
// history, is deleted.
func (a *App) chatConfirm(c *ui.Context, cn *conn, m locales.Messages) {
	if cn.ai.drop == "" {
		return
	}
	open := true
	title, body, ok := m.AiClearChatTitle(), m.AiClearChatBody(), m.AiClear()
	if cn.ai.drop != cn.ai.conv {
		title, body, ok = m.AiDeleteChatTitle(), m.AiDeleteChatBody(), m.Delete()
	}
	switch ui.AlertDialog(c.Key("aidrop:"+strconv.Itoa(cn.id)), &open, title, body, m.Cancel(), ok) {
	case 1:
		if cn.ai.drop == cn.ai.conv {
			a.ClearChat(cn.id)
		} else {
			a.DeleteChat(cn.host.ID, cn.ai.drop)
		}
		cn.ai.drop = ""
	default:
		if !open {
			cn.ai.drop = ""
		}
	}
}

// recentChats lists, on an empty pane, the host's latest conversations
// to carry on.
func (a *App) recentChats(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	list := a.chats(cn.host.ID)
	if len(list) == 0 {
		return
	}
	list = list[:min(len(list), 3)]
	ui.Column(c.Key("airecent")).Width(300).MaxWidth(300).Margin(theme.Space(4), 0, 0, 0).Gap(2).Children(func() {
		ui.Text(c, m.AiRecentChats()).FontSize(theme.Caption).FontWeight(600).TextColor(tok.TextMuted).Padding(0, 8, 4, 8)
		now := time.Now()
		for i, s := range list {
			a.menuRow(c, tok, menuEntry{Lead: a.chatLead(c, tok, s), Label: chatTitle(m, s), InlineNote: chatWhen(s.Updated, now),
				Do: func() { a.OpenChat(cn.id, s.ID) }}, i, 0)
		}
	})
}
