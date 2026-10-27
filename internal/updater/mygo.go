package updater

import (
	"context"
	"errors"

	"github.com/egoist/mygo"
)

// MygoBackend is the Backend of the app: mygo.Updater, which reads the
// manifest `mygo build` publishes to the latest release of the repository
// in mygo.json (updates.github = Licoy/sshyard) and verifies signatures.
type MygoBackend struct{}

func (MygoBackend) Enabled() bool { return mygo.Updater.Enabled() }

func (MygoBackend) Check(ctx context.Context) (Release, error) {
	up, err := mygo.Updater.Check(ctx)
	if errors.Is(err, mygo.ErrUpdatesDisabled) {
		return nil, ErrDisabled
	}
	if err != nil || up == nil {
		return nil, err
	}
	return mygoRelease{up}, nil
}

func (MygoBackend) Relaunch() { mygo.App.Relaunch() }

type mygoRelease struct{ up *mygo.Update }

func (r mygoRelease) Version() string { return r.up.Version }
func (r mygoRelease) Notes() string   { return r.up.Notes }
func (r mygoRelease) Install(ctx context.Context, p func(int64, int64)) error {
	return r.up.Install(ctx, p)
}
