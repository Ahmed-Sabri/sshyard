package app

import (
	"strconv"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/migration"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) migrationSection(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	ui.Text(c, m.MigrationReadOnly()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
	settingsSection(c, tok, icons.Import, tok.Accent, m.MigrationGeneral(), func(g *settingsGroup) {
		g.row("OpenSSH", m.MigrationFormat("~/.ssh/config"), func() {
			if (IconButton{Icon: icons.Import, Label: m.ImportSSHConfig()}).Clicked(c, "migration:openssh") {
				a.OpenImport(m, "")
			}
			if a.p.PickFile != nil && (IconButton{Icon: icons.OpenFile, Label: m.ImportChooseFile()}).Clicked(c, "migration:openssh:file") {
				a.pickImportFile(m)
			}
		})
	})
	sources := migration.Sources()
	groups := []struct {
		title      string
		start, end int
	}{{m.MigrationCommon(), 0, 3}, {m.MigrationCrossPlatform(), 3, 7}, {m.MigrationOther(), 7, 10}}
	formats := []string{".xsh", ".mxtsessions / .ini", ".reg / ~/.putty/sessions/", "user.sessions / .json", ".json", ".yaml / .yml", ".json / .csv", ".remmina", ".xml / .csv", ".ini"}
	for _, group := range groups {
		settingsSection(c, tok, icons.Import, tok.Accent, group.title, func(g *settingsGroup) {
			for i := group.start; i < group.end; i++ {
				source := sources[i]
				g.row(string(source), m.MigrationFormat(formats[i]), func() {
					if a.p.PickFiles != nil && (IconButton{Icon: icons.OpenFile, Label: m.MigrationChooseSourceFiles(string(source))}).Clicked(c, "migration:files:"+string(source)) {
						a.readMigration(m, source, false)
					}
					if (source == migration.Xshell || source == migration.PuTTY || source == migration.Remmina) && a.p.PickFolder != nil && (IconButton{Icon: icons.Folder, Label: m.MigrationChooseSourceFolder(string(source))}).Clicked(c, "migration:folder:"+string(source)) {
						a.readMigration(m, source, true)
					}
				})
			}
		})
	}
}
func (a *App) migrationView(c *ui.Context, m locales.Messages) {
	f := a.migration
	if f == nil {
		return
	}
	tok := currentTokens(c)
	width, height := c.Size()
	sheet(ui.Modal(c, &a.migrationOpen, func() {
		ui.Column(c).Width(min(float32(720), max(float32(220), width-theme.Space(24)))).Gap(theme.Space(3)).Children(func() {
			sheetTitle(c, m.MigrationPreview(string(f.source)))
			switch {
			case f.reading:
				ui.Text(c, m.MigrationReading()).FontSize(theme.Body)
			case f.saving:
				ui.Text(c, m.MigrationSaving()).FontSize(theme.Body)
			default:
				a.migrationPreview(c, m, tok, f, max(float32(100), height-theme.Space(105)))
			}
			if f.err != "" {
				ui.Text(c, f.err).TextColor(tok.Danger).FontSize(theme.Footnote)
			}
			ui.Row(c).Gap(theme.Space(2)).FillWidth().Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, f.saving) {
					a.closeMigration()
				}
				if pill(c, m.ImportButton(strconv.Itoa(f.count())), true, false, f.reading || f.saving || f.count() == 0) {
					a.startMigrationSave(m)
				}
			})
		})
	}), tok)
	if !a.migrationOpen {
		if f.saving {
			a.migrationOpen = true
		} else {
			a.closeMigration()
		}
	}
}
func (a *App) migrationPreview(c *ui.Context, m locales.Messages, tok theme.Tokens, f *migrationForm, height float32) {
	ui.Text(c, m.MigrationReadOnly()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
	in := ui.TextInput(c.Key("migration:search"), &f.search).Label(m.SearchHosts()).Placeholder(m.SearchHosts()).AutoFocus().FillWidth()
	a.migrationHold.apply(c, f, in)
	ui.Row(c).Gap(theme.Space(2)).FillWidth().Children(func() {
		if pill(c, m.MigrationSelectAll(), false, false, false) {
			f.selectVisible(true)
		}
		if pill(c, m.MigrationSelectNone(), false, false, false) {
			f.selectVisible(false)
		}
	})
	ui.Row(c).Gap(theme.Space(2)).FillWidth().Children(func() {
		ui.TextInput(c.Key("migration:group"), &f.group).Label(m.FieldGroup()).Placeholder(m.FieldGroup()).Grow(1).Shrink(1)
		if pill(c, m.MigrationApplyGroup(), false, false, false) {
			for _, r := range f.rows {
				if r.selected {
					r.group = f.group
				}
			}
		}
	})
	ui.Checkbox(c.Key("migration:secrets"), &f.saveSecrets, m.MigrationSaveSecrets())
	ui.Text(c, m.MigrationSecretsNote()).FontSize(theme.Caption).TextColor(tok.TextMuted)
	ui.Scroll(c.Key("migration:rows")).MaxHeight(height).FillWidth().Children(func() {
		ui.Column(c).Gap(theme.Space(2)).FillWidth().Children(func() {
			for _, issue := range f.issues {
				warnLine(c, tok, migrationIssue(m, issue))
			}
			for i, r := range f.rows {
				if f.visible(r) {
					a.migrationRowView(c, m, tok, f, r, i)
				}
			}
		})
	})
}
func (a *App) migrationRowView(c *ui.Context, m locales.Messages, tok theme.Tokens, f *migrationForm, r *migrationRow, i int) {
	key := "migration:row:" + strconv.Itoa(i)
	ui.Column(c.Key(key)).Padding(theme.Space(2)).Gap(theme.Space(2)).FillWidth().Radius(theme.RadiusControl).Border(1, tok.Line).Children(func() {
		ui.Row(c).Gap(theme.Space(2)).FillWidth().Children(func() {
			ui.Checkbox(c.Key(key+":pick"), &r.selected, "").Label(m.ImportPick(r.name))
			ui.TextInput(c.Key(key+":name"), &r.name).Label(m.FieldName()).Grow(1).Shrink(1)
		})
		ui.Text(c, r.entry.User+"@"+r.entry.Address+":"+strconv.Itoa(r.entry.Port)).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine().Shrink(1)
		ui.TextInput(c.Key(key+":group"), &r.group).Label(m.FieldGroup()).Placeholder(m.FieldGroup()).FillWidth()
		conflicts, _ := f.choices()
		if conflicts[r] {
			ui.Text(c, m.MigrationConflict()).FontSize(theme.Caption).TextColor(tok.Warning)
			labels := importActions(m)
			action := labels[int(r.action)]
			ui.Select(c.Key(key+":action"), &action, labels).Label(m.ImportConflictAction()).FillWidth()
			for i, label := range labels {
				if action == label {
					r.action = migrationAction(i)
				}
			}
		}
		for _, issue := range r.entry.Issues {
			warnLine(c, tok, migrationIssue(m, issue))
		}
		if len(r.entry.Jumps) > 0 {
			ui.Text(c, m.MigrationJumpCount(strconv.Itoa(len(r.entry.Jumps)))).FontSize(theme.Footnote).TextColor(tok.TextMuted)
		}
		if f.saveSecrets && (len(r.entry.Password) > 0 || len(r.entry.PrivateKey) > 0 || r.entry.KeyPath != "") {
			ui.Checkbox(c.Key(key+":secret"), &r.saveSecret, m.MigrationSaveRowSecrets())
		}
		if f.saveSecrets && (len(r.entry.PrivateKey) > 0 || r.entry.KeyPath != "") {
			ui.Text(c, r.entry.KeyPath).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
			if a.p.PickFile != nil && (IconButton{Icon: icons.OpenFile, Label: m.MigrationRelocateKey()}).Clicked(c, key+":key") {
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
							r.entry.KeyPath = path
							r.entry.PrivateKey = nil
						}
					})
				}()
			}
			ui.TextInput(c.Key(key+":pass"), &r.pass).Password().Label(m.FieldPassphrase()).Placeholder(m.FieldPassphrase()).FillWidth()
		}
	})
}
