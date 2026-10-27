# AGENTS.md

Global rules for anyone (human or agent) changing SSHYard. Read this before you write code.

## Hard constraints

- **Pure native UI.** Everything is drawn with MyGo `ui` (v0.6.4). No webview, no HTML, no xterm.js, and no HTML fallback of any kind.
- **Brand colour #1984FC.** Take colours from the theme (`c.Theme()`, `internal/theme`), never hard-coded hex in views. Every view must work in light and dark.
- **Every user-visible string in both `en` and `zh-Hans`**: labels, tooltips, accessible labels, errors and menu items. Add the key to `locales/en.json` and `locales/zh-Hans.json`, regenerate the catalog and use the generated accessor (`m.OpenFilesHere()`). No other languages.
- **Secrets only in the keychain** (`internal/keychain`, through `mygo.Secrets`). Never write a password, passphrase or private key to JSON, settings, logs or test fixtures on disk (except sealed in a vault, below), and never log key material. Secrets the user types for one run stay in memory. If the keychain is unavailable, the fallback is memory only. Deleting anything that owns a secret also deletes its keychain entries.
- **The one way out of the keychain is an end-to-end encrypted vault** (`internal/vault`): backups and, later, sync. Secrets leave the device only sealed in a vault file, whose key is random and wrapped under a key derived (Argon2id) from a password the user sets. Encrypting, decrypting and deriving happen on the device. Never store the vault password, in plaintext or encrypted; keep the derived or vault key only in the keychain. A backup or sync includes secrets by default, with an option to leave them out. Restoring writes secrets back into the keychain and takes only the secrets of the entries the vault itself holds.
- **Never commit key files** (`*.key` is ignored; the updater signing key stays outside the repo).
- **The version lives only in `mygo.json`.**

## Icons: Remix Icon only

- All icons come from [Remix Icon](https://remixicon.com/) **v4.8.0**, vendored in `internal/icons/svg/` (Apache-2.0; `internal/icons/LICENSE` and `NOTICE` must stay). Do not upgrade past 4.8.0 without checking the licence: 4.9+ uses the "Remix Icon License v1.0".
- No other icon set, no icon fonts, no hand-drawn SVG paths in views.
- Use the `-line` style. Use the matching `-fill` only for an active state, passed as `IconButton.On`.
- To add an icon:
  1. Copy `icons/<Category>/<name>.svg` from the RemixIcon `v4.8.0` tag into `internal/icons/svg/`, unchanged.
  2. Add a variable in `internal/icons/icons.go` named by meaning, not by shape: `Upload = load("upload-2-line")`.
  3. Run `go test ./internal/icons/`. If the icon has an On state, add the pair to `TestIconNames` in `internal/app/iconbutton_test.go`.

## App icon

- The source is `icon/logo.jpg`. `resources/icon.png` (Windows, Linux and the `.icns` fallback), `icon/Assets.car` (macOS, `macos.icon`) and `resources/icon-dev.png` (the `mygo dev` app, not shipped) are generated from it: never edit them by hand. After changing the logo, run `go run icon/gen.go` on a Mac with Xcode and commit the logo and every generated file.
- Keep sources out of `resources/`: everything there ships with the app.

## IconButton vs text Button

- Every icon-only action is an `IconButton` (`internal/app/iconbutton.go`). Do not build ad-hoc clickable icons with `ui.ButtonBase` or `ui.Icon` + click handling.
- `Label` is required and comes from the locale catalog. It is both the tooltip and the accessible label, so it names the action, not the icon.
- Set `Active` (and `On`) for a state that is on, `Toggle` for a toggle, `Disabled` when the action cannot run, `Danger` for destructive actions, `Size: Small` in dense rows.
- Hover, press, active and focus styles come from the theme accent. Do not restyle per call site.
- A text `Button` / `PrimaryButton` is only for the primary actions of a dialog or form.

## Design system

- Reference: macOS 27 (Liquid Glass). The accent is always #1984FC.
- Type scale only (`Caption 11, Footnote 12, Body 13, Title3 15, Title2 17, Title1 22`); spacing only via `theme.Space(n)` (4px grid); radii only via the `Radius*` tokens; shadows only via the `Shadow.*` tokens. New tokens need a test in `internal/theme/theme_test.go`.
- Identity tints come from `theme.Tint(id)` / `TintAt(i)`. Banners are rounded, tinted at α .12 with a matching border and a Remix icon.
- Sheets and dialogs use `sheet()` in `internal/app/style.go`. Their first field must autofocus and use a `focusHold` so typing works without a click. Buttons in sheets and settings are pills (`Capsule`).
- Glass without blur: MyGo has no backdrop blur except macOS vibrancy, so glass is approximated with alpha fills, hairlines and soft shadows. Do not fake blur with images or a webview.

## Keyboard

- App-wide shortcuts use `appMods` (⌘ on macOS, Ctrl+Shift elsewhere) so plain Ctrl+letter always reaches the shell. A new app shortcut must go through `isAppShortcut` so the terminal yields it.
- Do not take a platform's standard editing shortcuts (⌘A, ⌘C, ⌘V and their equivalents).

## Tests and checks

- UI changes need a test with `ui.Tester` in `internal/app`. The tester's `HasText`/`Texts` include accessible labels, so count occurrences when checking tooltips. A field bound to a TextInput is overwritten from the input's own state on the next frame; drive such flows by calling the handler rather than setting the field and clicking.
- Keep `go test -race`, `go vet` and `gofmt` clean in every commit. Before every push run:

```sh
go test -race -count=1 ./...
go vet ./...
GOOS=windows go vet ./...
GOOS=darwin go vet ./...
gofmt -l .            # must print nothing

# after editing locales/*.json
GOFLAGS=-mod=mod go run github.com/egoist/mygo/cmd/mygo generate
```

- Build only with MyGo's own `go tool mygo build`, never hand-rolled packaging:

```sh
MYGO_UPDATER_PRIVATE_KEY="$(cat path/to/mygo-update.key)" \
go tool mygo build \
  -platform linux/amd64,windows/amd64,darwin/arm64,darwin/amd64 -o dist
```

## CI and releases

- CI is GitHub Actions (`.github/workflows/ci.yml`, `release.yml`); keep both green. `ci.yml` runs only on `v*` tags and by hand, never on pushes or pull requests (private repo, billed minutes), so the local checks above are the gate.
- `v<version>` is a stable release: `release.yml` builds and uploads into a **draft** release (it needs the secret `MYGO_UPDATER_PRIVATE_KEY`); publish the draft by hand.
- `v<version>-beta-<yyyymmddhhmmss>` is an internal beta: only `ci.yml` runs, it never makes a Release and beta builds have no update channel. The timestamp must be a real UTC time in 2020–2099.
- The base `<version>` of any tag must equal `mygo.json`'s version, or the build fails.
- To release: bump `mygo.json` `version`, push, then tag. Agents never push tags themselves.
