package infra

import (
	"context"

	"github.com/pkg/errors"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/coldstore"
)

// Coldstore opens the local cold storage when it is enabled, and fails the startup when its files are not all
// there (see coldstore.OpenReader). It returns nil when the cold storage is disabled: users must check.
func Coldstore(lc fx.Lifecycle, conf *appconfig.Config) (*coldstore.Reader, error) {
	if !conf.ColdstoreEnabled {
		return nil, nil
	}
	if conf.ColdstoreDirectory == "" {
		return nil, errors.New("PENGUIN_V3_COLDSTORE_DIRECTORY is required when the cold storage is enabled")
	}
	r, err := coldstore.OpenReader(coldstore.ReaderConfig{
		Dir:                    conf.ColdstoreDirectory,
		Threads:                conf.ColdstoreThreads,
		MemoryLimit:            conf.ColdstoreMemoryLimit,
		TempDir:                conf.ColdstoreTempDirectory,
		ReloadInterval:         conf.ColdstoreReloadInterval,
		IntegrityCheckInterval: conf.ColdstoreIntegrityCheckInterval,
	})
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			r.Start()
			return nil
		},
		OnStop: func(context.Context) error {
			return r.Close()
		},
	})
	return r, nil
}
