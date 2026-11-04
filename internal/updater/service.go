package updater

// Backend is the update window of the app: MyGo's updater plugin, which
// checks soon after launch and every interval after, shows the release
// notes of a newer version in its own window and installs it; a fake in
// tests.
type Backend interface {
	// Enabled reports whether this build can update itself: development
	// builds and those installed by a package manager cannot.
	Enabled() bool
	// Check checks as the user asked: the update window shows at once and
	// says what it found.
	Check()
	// SetAutomatic turns the checks in the background on or off.
	SetAutomatic(on bool)
	// Available is the newer version the last check found, or "".
	Available() string
	// OnChange calls fn on the main thread after Available may have
	// changed, and returns a function that removes it.
	OnChange(fn func()) (off func())
}
