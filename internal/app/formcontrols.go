package app

import (
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo/ui"
)

// quietControl shares the quiet pill's fill without fading its text.
func quietControl(c *ui.Context, el ui.Element) ui.Element {
	tok := currentTokens(c)
	formPointer(el).Radius(theme.Capsule).TextColor(tok.Text)
	switch {
	case el.IsDisabled():
		el.Background(tok.Fill)
	case el.Pressed():
		el.Background(tok.Text.Alpha(0.14))
	case el.Hovered():
		el.Background(tok.Text.Alpha(0.10))
	default:
		el.Background(tok.Fill)
	}
	return el
}

func formSelect(c *ui.Context, selected *string, options []string) ui.Element {
	return formSelectContent(c, selected, options, func(label string) {
		ui.Text(c, label).FontSize(theme.Body).TextColor(currentTokens(c).Text).SingleLine().Grow(1).Shrink(1).MinWidth(0)
	})
}

// SelectBase retains the native popup, keyboard, focus and Changed semantics.
func formSelectContent(c *ui.Context, selected *string, options []string, content func(string)) ui.Element {
	tok := currentTokens(c)
	sel := ui.SelectBase(c, selected)
	quietControl(c, sel.Trigger).Padding(theme.Space(1.5), theme.Space(3.5)).Gap(theme.Space(1.5)).MinWidth(theme.Space(35)).Children(func() {
		content(*selected)
		ui.Icon(c, icons.Collapse.SVG).Size(12, 12).TextColor(tok.TextMuted).Shrink(0)
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(theme.Space(1)).Radius(theme.RadiusCard).Background(tok.Inset).Border(1, tok.Line).MaxHeight(menuHeight)
		gutterScroll(c, ui.Scroll(c).MaxHeight(menuHeight)).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for _, option := range options {
					c.Key(option)
					item := formDisabled(sel.Item(option), false).Padding(theme.Space(1.5), theme.Space(2)).Gap(theme.Space(1.5)).Radius(theme.RadiusSmall)
					if item.Highlighted() {
						item.Background(tok.Accent.Alpha(0.12))
					} else if option == *selected {
						item.Background(tok.Fill)
					}
					item.Children(func() { content(option) })
				}
			})
		})
	})
	return sel.Trigger
}

func formSwitch(c *ui.Context, on *bool) ui.Element {
	return formDisabled(ui.Switch(c, on), false)
}

func formCheckbox(c *ui.Context, checked *bool, label string) ui.Element {
	return formDisabled(ui.Checkbox(c, checked, label), false)
}

// MyGo's disabled nodes keep an explicit cursor, so set both together.
func formDisabled(el ui.Element, disabled bool) ui.Element {
	el.Disabled(disabled)
	return formPointer(el)
}

func formPointer(el ui.Element) ui.Element {
	if el.IsDisabled() {
		return el.Cursor(ui.CursorDefault)
	}
	return el.Cursor(ui.CursorPointer)
}

func formTextInput(c *ui.Context, value *string) ui.Element {
	return ui.TextInput(c, value).Background(currentTokens(c).Fill).Radius(theme.RadiusControl)
}

func formTextArea(c *ui.Context, value *string) ui.Element {
	return ui.TextArea(c, value).Background(currentTokens(c).Fill).Radius(theme.RadiusControl)
}

func formCombobox(c *ui.Context, selected *string, options []string) ui.Element {
	return ui.Combobox(c, selected, options).Background(currentTokens(c).Fill).Radius(theme.RadiusControl)
}

func formSegmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	return formSegmentedState(c, selected, false, labels...)
}

func formSegmentedState(c *ui.Context, selected *int, disabled bool, labels ...string) ui.Element {
	parts := ui.SegmentedBase(c, selected, len(labels))
	parts.Track.Disabled(disabled)
	quietControl(c, parts.Track).AlignItems(ui.Stretch).Padding(theme.Space(0.5)).Gap(theme.Space(0.5)).Children(func() {
		for i, label := range labels {
			segment := quietControl(c, parts.Segment(i)).Label(label).Padding(theme.Space(1.5), theme.Space(3.5))
			if i == *selected {
				segment.Background(currentTokens(c).Accent.Alpha(0.12))
			}
			segment.Children(func() { ui.Text(c, label).FontSize(theme.Body).TextColor(currentTokens(c).Text).SingleLine() })
		}
	})
	return parts.Track
}
