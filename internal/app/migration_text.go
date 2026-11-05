package app

import (
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// A text import reads a host list of key=value lines, typed or pasted
// into the text area or read from a file, into a preview where every
// field of every row is edited, alone or for the selected rows at once,
// and checked as it is; the import saves the rows only once all are
// right, then shows what it did.

// migrationTextExample is the text area's placeholder: the format itself.
const migrationTextExample = "host=192.168.1.10 port=22 user=root password=secret title=web-01\n" +
	"ip=10.0.0.5 username=admin label=\"db 01\""

// migrationBatch edits the selected rows of a text import at once. A
// field left unchanged (group and login not set, user and port blank)
// keeps each row's own, as a blank password or key keeps each row's.
type migrationBatch struct {
	open             bool
	group, newGroup  string
	groupSet, adding bool
	user, port       string
	auth             assets.AuthMethod
	cred             string
	password, pass   string
	keyPath, err     string
}

// migrationCheck is a text import's rows as they stand: those in conflict
// with a saved host, and why any of those to be saved cannot be, with the
// field to correct. blocking counts the rows whose fields are wrong, which
// stop the import; a row's problem from the last save, with its key,
// shows until the row changes.
type migrationCheck struct {
	conflicts map[*migrationRow]bool
	saved     map[string]assets.Host
	problems  map[*migrationRow]string
	fields    map[*migrationRow]string
	blocking  int
}

func (a *App) checkMigration(m locales.Messages, f *migrationForm) migrationCheck {
	conflicts, final := f.choices()
	k := migrationCheck{conflicts: conflicts, saved: f.saved(), problems: map[*migrationRow]string{}, fields: map[*migrationRow]string{}}
	for _, r := range final {
		if field, p := migrationProblem(m, r, f.overwrites(k.saved, r)); p != "" {
			k.problems[r], k.fields[r] = p, field
			k.blocking++
		} else if r.problem != "" {
			k.problems[r], k.fields[r] = r.problem, "secret"
		}
	}
	return k
}

// migrationProblem is why a text import's row cannot be saved, and the
// field to correct (address, port, user, auth, credential or secret), ""
// when it can; old is the saved host it overwrites, whose secret a blank
// password or key of the same login keeps.
func migrationProblem(m locales.Messages, r *migrationRow, old *assets.Host) (field, problem string) {
	address := strings.TrimSpace(r.entry.Address)
	if strings.ContainsAny(address, " \t/@") || strings.Contains(address, ":") && !validAddr(address) {
		return "address", m.MigrationInvalidHost()
	}
	h := assets.Host{Address: address, Port: r.entry.Port, User: r.entry.User, Auth: r.auth, CredentialID: r.cred}
	h.Normalize()
	if err := h.Validate(); err != nil {
		var fe *assets.FieldError
		if errors.As(err, &fe) {
			field = fe.Field
		}
		return field, fieldError(m, err)
	}
	keep := old != nil && old.Auth == r.auth
	switch {
	case r.auth == assets.AuthPassword && r.password == "" && !keep:
		return "secret", m.CredPasswordRequired()
	case r.auth == assets.AuthKey && len(r.entry.PrivateKey) == 0 && r.entry.KeyPath == "" && !keep:
		return "secret", m.MigrationRelocateKey()
	}
	return "", ""
}

func validAddr(s string) bool { _, err := netip.ParseAddr(s); return err == nil }

// migrationPort is the port typed, -1 when it is none.
func migrationPort(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return -1
	}
	return n
}

// migrationGroups are the groups to choose for a text import's rows: the
// saved hosts', then those the rows and the batch edit name.
func (a *App) migrationGroups(f *migrationForm) []string {
	groups := a.groups()
	add := func(g string) {
		if g != "" && !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}
	for _, r := range f.rows {
		add(r.group)
	}
	if f.batch.groupSet {
		add(f.batch.group)
	}
	return groups
}

// migrationAuthLabel is the login's label among choices, "" when gone.
func migrationAuthLabel(choices []authChoice, method assets.AuthMethod, cred string) string {
	for _, ch := range choices {
		if ch.method == method && ch.cred == cred {
			return ch.label
		}
	}
	return ""
}

// pickMigrationKey asks for a private key file and hands its path to set,
// unless the preview has closed or is saving meanwhile.
func (a *App) pickMigrationKey(m locales.Messages, f *migrationForm, set func(path string)) {
	go func() {
		path, err := a.p.PickFile(m.MigrationRelocateKey())
		a.post(func() {
			if a.migration != f || f.saving {
				return
			}
			if err != nil {
				f.err = m.MigrationKeyError()
				return
			}
			if path != "" {
				set(path)
			}
		})
	}()
}

// migrationComposer is the text area the host list is typed or pasted in.
func (a *App) migrationComposer(c *ui.Context, m locales.Messages, tok theme.Tokens, f *migrationForm, height float32) {
	ui.Text(c, m.MigrationTextHelp()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
	in := ui.TextArea(c.Key("migration:text"), &f.text).Label(m.MigrationTextLabel()).Placeholder(migrationTextExample).
		Font(a.termFamily()).AutoFocus().FillWidth().Height(min(height, theme.Space(80)))
	a.migrationHold.apply(c, f, in)
}

// applyMigrationBatch sets the batch edit's fields on the selected rows.
func (a *App) applyMigrationBatch(m locales.Messages, f *migrationForm) {
	b := &f.batch
	b.err = ""
	port := 0
	if strings.TrimSpace(b.port) != "" {
		if port = migrationPort(b.port); port < 0 {
			b.err = m.InvalidPort()
			return
		}
	}
	user := strings.TrimSpace(b.user)
	for _, r := range f.rows {
		if !r.selected {
			continue
		}
		if b.groupSet {
			r.group = b.group
		}
		if user != "" {
			r.entry.User = user
		}
		if port > 0 {
			r.port, r.entry.Port = strconv.Itoa(port), port
		}
		if b.auth == "" {
			continue
		}
		r.auth, r.cred, r.problem = b.auth, b.cred, ""
		switch {
		case b.auth == assets.AuthPassword && b.password != "":
			r.password = b.password
		case b.auth == assets.AuthKey && b.keyPath != "":
			clear(r.entry.PrivateKey)
			r.entry.KeyPath, r.entry.PrivateKey, r.pass = b.keyPath, nil, b.pass
		}
	}
}

// migrationBatchView edits the selected rows of a text import at once:
// a row of fields under their names, wrapping when the sheet is narrow.
func (a *App) migrationBatchView(c *ui.Context, m locales.Messages, tok theme.Tokens, f *migrationForm, choices []authChoice, groups []string) {
	b := &f.batch
	ui.Collapsible(c.Key("migration:batch"), m.MigrationBatchEdit(), &b.open, func() {
		ui.Column(c).Gap(theme.Space(2)).FillWidth().Margin(theme.Space(2), 0, 0, 0).Children(func() {
			ui.Row(c).Wrap().Gap(theme.Space(3)).AlignItems(ui.End).FillWidth().Children(func() {
				migrationBatchField(c, tok, m.FieldGroup(), theme.Space(56), func() { a.migrationBatchGroup(c, m, b, groups) })
				migrationBatchField(c, tok, m.FieldUser(), theme.Space(36), func() {
					ui.TextInput(c.Key("migration:batch:user"), &b.user).Label(m.FieldUser()).Placeholder(m.MigrationUnchanged()).FillWidth()
				})
				migrationBatchField(c, tok, m.FieldPort(), theme.Space(24), func() {
					ui.TextInput(c.Key("migration:batch:port"), &b.port).Label(m.FieldPort()).Placeholder(m.MigrationUnchanged()).Error(b.err).FillWidth()
				})
				migrationBatchField(c, tok, m.FieldAuth(), theme.Space(44), func() {
					labels := []string{m.MigrationUnchanged()}
					for _, ch := range choices {
						labels = append(labels, ch.label)
					}
					label := migrationAuthLabel(choices, b.auth, b.cred)
					if b.auth == "" || label == "" {
						label = m.MigrationUnchanged()
					}
					if ui.Select(c.Key("migration:batch:auth"), &label, labels).Label(m.FieldAuth()).FillWidth().Changed() {
						b.auth, b.cred = "", ""
						for _, ch := range choices {
							if ch.label == label {
								b.auth, b.cred = ch.method, ch.cred
							}
						}
					}
				})
				switch b.auth {
				case assets.AuthPassword:
					migrationBatchField(c, tok, m.FieldPassword(), theme.Space(44), func() {
						ui.TextInput(c.Key("migration:batch:password"), &b.password).Password().Label(m.FieldPassword()).Placeholder(m.MigrationUnchanged()).FillWidth()
					})
				case assets.AuthKey:
					migrationBatchField(c, tok, m.FieldPrivateKey(), theme.Space(40), func() {
						a.migrationKeyField(c, m, tok, f, "migration:batch:key", b.keyPath, m.MigrationUnchanged(), func(path string) { b.keyPath = path })
					})
					migrationBatchField(c, tok, m.FieldPassphrase(), theme.Space(40), func() {
						ui.TextInput(c.Key("migration:batch:pass"), &b.pass).Password().Label(m.FieldPassphrase()).Placeholder(m.FieldPassphrase()).FillWidth()
					})
				}
				selected := slices.ContainsFunc(f.rows, func(r *migrationRow) bool { return r.selected })
				if pill(c, m.MigrationApplyGroup(), false, false, !selected) {
					a.applyMigrationBatch(m, f)
				}
			})
			if b.err != "" {
				ui.Text(c, b.err).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
		})
	})
}

// migrationBatchField is a field of the batch edit, width wide, under its
// name.
func migrationBatchField(c *ui.Context, tok theme.Tokens, label string, width float32, fn func()) {
	ui.Column(c).Gap(theme.Space(1)).Width(width).Shrink(1).MinWidth(0).Children(func() {
		ui.Text(c, label).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine()
		fn()
	})
}

// migrationBatchGroup chooses the batch edit's group, or names a new one.
func (a *App) migrationBatchGroup(c *ui.Context, m locales.Messages, b *migrationBatch, groups []string) {
	ui.Row(c).Gap(theme.Space(1)).AlignItems(ui.Center).FillWidth().Children(func() {
		if b.adding {
			in := ui.TextInput(c.Key("migration:batch:newgroup"), &b.newGroup).Label(m.NewGroup()).Placeholder(m.NewGroup()).AutoFocus().Grow(1).Shrink(1)
			use := (IconButton{Icon: icons.Check, Label: m.MigrationUseGroup(), Disabled: strings.TrimSpace(b.newGroup) == ""}).Clicked(c, "migration:batch:newgroup:use")
			if (in.Submitted() || use) && strings.TrimSpace(b.newGroup) != "" {
				name := strings.TrimSpace(b.newGroup)
				// A name differing only in case is the group already there.
				for _, g := range groups {
					if strings.EqualFold(g, name) {
						name = g
					}
				}
				b.group, b.groupSet, b.adding, b.newGroup = name, true, false, ""
			}
			if (IconButton{Icon: icons.Close, Label: m.Cancel()}).Clicked(c, "migration:batch:newgroup:cancel") {
				b.adding, b.newGroup = false, ""
			}
			return
		}
		label := m.MigrationUnchanged()
		switch {
		case b.groupSet && b.group == "":
			label = m.NoGroup()
		case b.groupSet:
			label = b.group
		}
		options := append([]string{m.MigrationUnchanged(), m.NoGroup()}, groups...)
		if ui.Combobox(c.Key("migration:batch:group"), &label, options).Label(m.FieldGroup()).Grow(1).Shrink(1).Changed() {
			b.groupSet, b.group = label != m.MigrationUnchanged(), label
			if label == m.NoGroup() || !b.groupSet {
				b.group = ""
			}
		}
		if (IconButton{Icon: icons.NewFolder, Label: m.NewGroup()}).Clicked(c, "migration:batch:group:new") {
			b.adding, b.newGroup = true, ""
		}
	})
}

// migrationKeyField shows the key file chosen, or none while path is
// blank, and a button choosing another.
func (a *App) migrationKeyField(c *ui.Context, m locales.Messages, tok theme.Tokens, f *migrationForm, key, path, none string, set func(string)) {
	ui.Row(c).Gap(theme.Space(2)).AlignItems(ui.Center).FillWidth().Children(func() {
		if a.p.PickFile != nil && (IconButton{Icon: icons.OpenFile, Label: m.MigrationRelocateKey()}).Clicked(c, key) {
			a.pickMigrationKey(m, f, set)
		}
		name := none
		if path != "" {
			name = filepath.Base(path)
		}
		if name != "" {
			ui.Text(c, name).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine().Shrink(1)
		}
	})
}

// migrationTable is a text import's rows in view, a field to a column
// under its name: every field edits, the field a row's problem names is
// marked, and the last column says what the import makes of the row.
func (a *App) migrationTable(c *ui.Context, m locales.Messages, tok theme.Tokens, f *migrationForm, k migrationCheck, choices []authChoice, groups []string) {
	var rows []int
	for i, r := range f.rows {
		if f.visible(r) {
			rows = append(rows, i)
		}
	}
	// A row's fields follow it as the search hides the rows before it.
	f.table.Key = func(row int) any { return f.rows[rows[row]] }
	// At their least the columns fit a sheet in a 1000-wide window; in a
	// narrower one the table scrolls sideways. The status, whose lines
	// wrap, keeps its width as the others share what a wider sheet adds.
	cols := []ui.TableColumn{
		{ID: "pick", Width: theme.Space(8), Fixed: true},
		{Title: m.FieldName(), ID: "name", MinWidth: theme.Space(24)},
		{Title: m.FieldHost(), ID: "address", MinWidth: theme.Space(26)},
		{Title: m.FieldPort(), ID: "port", Width: theme.Space(16)},
		{Title: m.FieldUser(), ID: "user", MinWidth: theme.Space(18)},
		{Title: m.FieldGroup(), ID: "group", MinWidth: theme.Space(24)},
		{Title: m.FieldAuth(), ID: "auth", MinWidth: theme.Space(30)},
		{Title: m.MigrationColSecret(), ID: "secret", MinWidth: theme.Space(32)},
		{Title: m.MigrationColStatus(), ID: "status", Width: theme.Space(48)},
	}
	ui.Table(c.Key("migration:table"), &f.table, cols, len(rows), func(row, col int) {
		i := rows[row]
		r := f.rows[i]
		key := "migration:row:" + strconv.Itoa(i)
		// wrong is the row's problem when it names field, "" when not.
		wrong := func(fields ...string) string {
			if slices.Contains(fields, k.fields[r]) {
				return k.problems[r]
			}
			return ""
		}
		switch cols[col].ID {
		case "pick":
			ui.Checkbox(c.Key(key+":pick"), &r.selected, "").Label(m.ImportPick(r.name))
		case "name":
			ui.TextInput(c.Key(key+":name"), &r.name).Label(m.FieldName()).Placeholder(m.FieldName()).Grow(1).MinWidth(0)
		case "address":
			ui.TextInput(c.Key(key+":host"), &r.entry.Address).Label(m.FieldHost()).Placeholder(m.FieldHost()).Error(wrong("address")).Grow(1).MinWidth(0)
		case "port":
			ui.TextInput(c.Key(key+":port"), &r.port).Label(m.FieldPort()).Placeholder(m.FieldPort()).Error(wrong("port")).Grow(1).MinWidth(0)
			r.entry.Port = migrationPort(r.port)
		case "user":
			ui.TextInput(c.Key(key+":user"), &r.entry.User).Label(m.FieldUser()).Placeholder(m.FieldUser()).Error(wrong("user")).Grow(1).MinWidth(0)
		case "group":
			group := r.group
			if group == "" {
				group = m.NoGroup()
			}
			if ui.Select(c.Key(key+":group"), &group, append([]string{m.NoGroup()}, groups...)).Label(m.FieldGroup()).Grow(1).MinWidth(0).Changed() {
				r.group = group
				if group == m.NoGroup() {
					r.group = ""
				}
			}
		case "auth":
			var labels []string
			for _, ch := range choices {
				labels = append(labels, ch.label)
			}
			label := migrationAuthLabel(choices, r.auth, r.cred)
			if label == "" {
				label = a.askLabel(m)
			}
			if ui.Select(c.Key(key+":auth"), &label, labels).Label(m.FieldAuth()).Error(wrong("auth", "credential")).Grow(1).MinWidth(0).Changed() {
				for _, ch := range choices {
					if ch.label == label {
						r.auth, r.cred, r.problem = ch.method, ch.cred, ""
					}
				}
			}
		case "secret":
			a.migrationSecretCell(c, m, tok, f, k, r, key, wrong("secret"))
		case "status":
			ui.Column(c).Gap(theme.Space(1)).Grow(1).MinWidth(0).Children(func() {
				if p := k.problems[r]; p != "" {
					migrationNote(c, icons.Warning, tok.Danger, p)
				}
				if k.conflicts[r] {
					migrationConflictView(c, m, tok, key, r)
				}
				for _, issue := range r.entry.Issues {
					migrationNote(c, icons.Warning, tok.Warning, migrationIssue(m, issue))
				}
				if r.selected && k.problems[r] == "" && !k.conflicts[r] && len(r.entry.Issues) == 0 {
					migrationNote(c, icons.Check, tok.Success, m.MigrationRowReady())
				}
			})
		}
	}).Grow(1).Shrink(1).MinHeight(theme.Space(40)).FillWidth()
}

// migrationSecretCell edits the secret of a row's login: its password, or
// its key file and the key's passphrase. A blank secret of the saved
// host's own login keeps its secret.
func (a *App) migrationSecretCell(c *ui.Context, m locales.Messages, tok theme.Tokens, f *migrationForm, k migrationCheck, r *migrationRow, key, wrong string) {
	old := f.overwrites(k.saved, r)
	password, none := m.FieldPassword(), ""
	if old != nil && old.Auth == r.auth {
		password, none = m.MigrationUnchanged(), m.MigrationUnchanged()
	}
	switch r.auth {
	case assets.AuthPassword:
		// In the clear, as the list it came from has it, to check it.
		ui.TextInput(c.Key(key+":password"), &r.password).Label(m.FieldPassword()).Placeholder(password).Error(wrong).Grow(1).MinWidth(0)
	case assets.AuthKey:
		ui.Column(c).Gap(theme.Space(1)).Grow(1).MinWidth(0).Children(func() {
			a.migrationKeyField(c, m, tok, f, key+":key", r.entry.KeyPath, none, func(path string) {
				clear(r.entry.PrivateKey)
				r.entry.KeyPath, r.entry.PrivateKey, r.problem = path, nil, ""
			})
			if ui.TextInput(c.Key(key+":pass"), &r.pass).Password().Label(m.FieldPassphrase()).Placeholder(m.FieldPassphrase()).FillWidth().Changed() {
				r.problem = ""
			}
		})
	}
}

// migrationNote is a line of what the import makes of a row.
func migrationNote(c *ui.Context, ic *icons.Icon, col ui.Color, s string) {
	ui.Row(c).Gap(theme.Space(1.5)).AlignItems(ui.Start).FillWidth().Children(func() {
		ui.Icon(c, ic.SVG).Size(12, 12).Shrink(0).Margin(2, 0, 0, 0).TextColor(col)
		ui.Text(c, s).FontSize(theme.Footnote).TextColor(col).Grow(1).Shrink(1).MinWidth(0)
	})
}

// migrationOutcomeView reports what a text import saved.
func migrationOutcomeView(c *ui.Context, m locales.Messages, tok theme.Tokens, o migrationOutcome) {
	col := tok.Success
	ui.Row(c.Key("migration:done")).FillWidth().Padding(theme.Space(3)).Gap(theme.Space(2)).AlignItems(ui.Center).
		Radius(theme.RadiusControl).Background(col.Alpha(0.12)).Border(1, col.Alpha(0.22)).Children(func() {
		ui.Icon(c, icons.Check.SVG).Size(16, 16).Shrink(0).TextColor(col)
		ui.Text(c, m.MigrationResult(strconv.Itoa(o.added), strconv.Itoa(o.updated), strconv.Itoa(o.skipped))).FontSize(theme.Body).Grow(1).Shrink(1)
	})
}
