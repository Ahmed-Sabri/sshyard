package app

import (
	"strings"

	"github.com/Licoy/sshyard/internal/aibrands"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func brandImage(c *ui.Context, icon *aibrands.Icon, size float32) {
	bitmap := icon.Light
	if currentTokens(c).Dark {
		bitmap = icon.Dark
	}
	ui.Image(c, bitmap()).Label(icon.Name).Size(size, size).Shrink(0)
}

func providerFormatControl(c *ui.Context, m locales.Messages, selected *int) ui.Element {
	tok := currentTokens(c)
	labels := []string{m.AiFormatChat(), m.AiFormatResponses(), m.AiFormatAnthropic(), m.AiFormatTypeSafe()}
	parts := ui.SegmentedBase(c, selected, len(labels))
	parts.Track.AlignItems(ui.Stretch).Padding(theme.Space(0.5)).Gap(theme.Space(0.5)).Radius(theme.Capsule).Background(tok.Inset).Border(1, tok.Line).Children(func() {
		for i, label := range labels {
			segment := parts.Segment(i).Label(label).Padding(theme.Space(1.5), theme.Space(1)).Gap(theme.Space(1)).Radius(theme.Capsule).Cursor(ui.CursorPointer)
			if i == *selected {
				segment.Background(tok.Accent.Alpha(0.12))
			}
			segment.Children(func() {
				brandImage(c, aibrands.Provider(settings.AIProvider{Format: providerFormats[i]}), 14)
				ui.Text(c, label).FontSize(theme.Footnote).TextColor(tok.Text).SingleLine()
			})
		}
	})
	return parts.Track.Label(m.AiFormat())
}

// brandSelect keeps the native select's pointer, keyboard and accessibility behavior.
func brandSelect(c *ui.Context, selected *string, options []string, brand func(string) *aibrands.Icon) ui.Element {
	tok := currentTokens(c)
	sel := ui.SelectBase(c, selected)
	content := func(label string) {
		if icon := brand(label); icon != nil {
			brandImage(c, icon, 16)
		}
		ui.Text(c, label).FontSize(theme.Body).TextColor(tok.Text).SingleLine().Grow(1).Shrink(1).MinWidth(0)
	}
	sel.Trigger.Padding(theme.Space(1.5), theme.Space(2.5)).Gap(theme.Space(1.5)).Radius(theme.RadiusSmall).Background(tok.Inset).Border(1, tok.Line).Children(func() {
		content(*selected)
		ui.Icon(c, icons.Collapse.SVG).Size(12, 12).TextColor(tok.TextMuted)
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(theme.Space(1)).Radius(theme.RadiusCard).Background(tok.Inset).Border(1, tok.Line).MaxHeight(menuHeight)
		ui.Scroll(c).MaxHeight(menuHeight).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for _, option := range options {
					c.Key(option)
					item := sel.Item(option).Padding(theme.Space(1.5), theme.Space(2)).Gap(theme.Space(1.5)).Radius(theme.RadiusSmall)
					if item.Highlighted() {
						item.Background(tok.Accent.Alpha(0.12))
					}
					item.Children(func() { content(option) })
				}
			})
		})
	})
	return sel.Trigger
}

func brandAutocomplete(c *ui.Context, value *string, models []string, provider settings.AIProvider) ui.Element {
	tok := currentTokens(c)
	cb := ui.ComboboxBase(c, value)
	in := cb.Input.Padding(theme.Space(1.5), theme.Space(2.5)).Radius(theme.RadiusSmall).Background(tok.Inset).Border(1, tok.Line)
	cb.Popup(func(panel ui.Element) {
		panel.Padding(theme.Space(1)).Radius(theme.RadiusCard).Background(tok.Inset).Border(1, tok.Line)
		ui.Scroll(c).MaxHeight(240).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for _, model := range models {
					if cb.Filtering() && !strings.Contains(strings.ToLower(model), strings.ToLower(strings.TrimSpace(*value))) {
						continue
					}
					c.Key(model)
					item := cb.Item(model).Padding(theme.Space(1.5), theme.Space(2)).Gap(theme.Space(1.5)).Radius(theme.RadiusSmall)
					if item.Highlighted() {
						item.Background(tok.Accent.Alpha(0.12))
					}
					item.Children(func() {
						brandImage(c, aibrands.Model(model, provider), 16)
						ui.Text(c, model).FontSize(theme.Body).TextColor(tok.Text).SingleLine()
					})
				}
			})
		})
	})
	if chosen, ok := cb.Chosen(); ok {
		*value = chosen
		in.SetChanged()
	}
	return in
}
