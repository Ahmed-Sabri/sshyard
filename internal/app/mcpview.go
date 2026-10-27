package app

import (
	"strconv"
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// mcpSettings is the AI page's MCP section: the switch, which asks
// first (mcpSheet), and while on, where it listens, the token and the
// connections agents hold.
func (a *App) mcpSettings(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	on := s.MCPEnabled
	if on {
		aiBanner(c, tok.Warning, icons.Warning, m.McpOnBanner())
	}
	settingsSection(c, tok, icons.MCP, tok.TintAt(5), m.McpSection(), func(g *settingsGroup) {
		g.row(m.McpEnable(), m.McpEnableNote(), func() {
			sw := on
			if ui.Switch(c.Key("mcpon"), &sw).Label(m.McpEnable()).Changed() {
				if sw {
					a.mcpAsk = &mcpAsk{open: true}
				} else {
					s.MCPEnabled = false
					*changed = true
				}
			}
		})
		if !on {
			return
		}
		g.row(m.FieldPort(), "", func() {
			if ui.NumberInput(c.Key("mcpport"), &a.mcpPort, 1024, 65535, 1).Label(m.FieldPort()).Changed() {
				s.MCPPort = int(a.mcpPort)
				*changed = true
			}
		})
		switch {
		case a.mcp.err != nil:
			g.row(m.McpFailed(a.mcp.err.Error()), "", func() {})
		case a.mcp.srv != nil:
			url := a.mcp.srv.URL()
			g.row(m.McpListening(url), "", func() {
				if iconButton(c, "mcp:url", icons.Copy, m.McpCopyURL()) {
					c.WriteClipboard(url)
					c.AddToast(ui.Toast{Title: m.Copied(), Type: "success"})
				}
			})
			g.row(m.McpToken(), m.McpTokenNote(), func() {
				ui.Text(c, "••••••••••••").FontSize(theme.Body).TextColor(tok.TextMuted)
				if iconButton(c, "mcp:token", icons.Copy, m.McpCopyToken()) {
					c.WriteClipboard(a.mcp.token)
					c.AddToast(ui.Toast{Title: m.Copied(), Type: "success"})
				}
				if (IconButton{Icon: icons.Refresh, Label: m.McpNewToken(), Danger: true}).Clicked(c, "mcp:newtoken") {
					if err := a.newMCPToken(); err != nil {
						c.AddToast(ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"})
					} else {
						c.AddToast(ui.Toast{Title: m.McpNewTokenDone(), Type: "success"})
					}
				}
			})
			g.divider()
			ui.Row(c).Padding(8, 14).Children(func() {
				if pill(c, m.McpCopyConfig(), false, false, false) {
					c.WriteClipboard(a.mcpConfig())
					c.AddToast(ui.Toast{Title: m.Copied(), Type: "success"})
				}
			})
		}
	})
	if !on {
		return
	}
	settingsSection(c, tok, icons.Host, tok.TintAt(2), m.McpSessions(), func(g *settingsGroup) {
		list := a.mcpSessionList()
		if len(list) == 0 {
			ui.Row(c).MinHeight(44).Padding(8, 14).Children(func() {
				ui.Text(c, m.McpNoSessions()).FontSize(theme.Body).TextColor(tok.TextMuted)
			})
		}
		for _, ss := range list {
			id := ss.id
			g.row(ss.host.Label(), a.pv(ss.host.Address)+" · "+ss.since.Format("15:04:05"), func() {
				if (IconButton{Icon: icons.Disconnect, Label: m.McpDisconnect(), Danger: true}).Clicked(c, "mcp:drop:"+id) {
					a.dropMCPSession(id)
				}
			})
		}
	})
}

// mcpSheet lays out the risks of the endpoint before it is turned on.
// The confirm button stays disabled for mcpWait after the sheet shows,
// counting down, so it cannot be clicked through unread.
func (a *App) mcpSheet(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	ask := a.mcpAsk
	if ask == nil {
		return
	}
	now := c.Now()
	if ask.at.IsZero() {
		ask.at = now
	}
	left := mcpWait - now.Sub(ask.at)
	secs := int((left + time.Second - 1) / time.Second)
	if left > 0 {
		// Next frame when the count changes.
		c.After(left - time.Duration(secs-1)*time.Second)
	}
	sheet(ui.Modal(c.Key("mcprisk"), &ask.open, func() {
		ui.Column(c).Gap(14).Width(500).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				tile(c, icons.Warning, tok.Danger, 36)
				sheetTitle(c, m.McpRiskTitle())
			})
			ui.Text(c, m.McpRiskIntro()).FontSize(theme.Body).FillWidth()
			ui.Column(c).Gap(10).FillWidth().Padding(12, 14).Radius(theme.RadiusControl).
				Background(tok.Danger.Alpha(0.12)).Border(1, tok.Danger.Alpha(0.22)).Children(func() {
				for _, risk := range []string{m.McpRiskCommands(), m.McpRiskNoAsk(), m.McpRiskInjection(), m.McpRiskSecrets(), m.McpRiskLocal()} {
					ui.Row(c).Gap(theme.Space(2)).FillWidth().Children(func() {
						ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(tok.Danger).Margin(2, 0, 0, 0)
						ui.Text(c, risk).FontSize(theme.Footnote).Grow(1).Shrink(1)
					})
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				el, cancel := pillEl(c, m.Cancel(), false, false, false)
				a.mcpHold.apply(c, ask, el)
				if cancel {
					ask.open = false
				}
				label := m.McpConfirm()
				if secs > 0 {
					label = m.McpConfirmWait(strconv.Itoa(secs))
				}
				if pill(c, label, false, true, secs > 0) {
					ask.open = false
					if err := a.setMCP(true); err != nil {
						c.AddToast(ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"})
					}
				}
			})
		})
	}), tok)
	if a.mcpAsk == ask && !ask.open {
		a.mcpAsk = nil
	}
}
