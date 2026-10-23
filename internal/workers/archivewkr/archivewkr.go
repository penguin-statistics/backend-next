// Package archivewkr runs the monthly archiving of the cold storage (#752, coldstore.Archiver) behind
// appconfig.ConfigSpec.ColdstoreArchiveEnabled: the months whose partitions are old enough are moved from PostgreSQL
// to the local cold storage directory and the backup bucket, and the partitions ahead are created.
package archivewkr

import (
	"context"
	"database/sql"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/coldstore"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

// LockName is the Redis lock that keeps two processes (or a process and run-script coldstore-archive) from
// archiving at once.
const LockName = "mutex:coldstore-archive"

const (
	// lockExpiry is the expiry of the lock, which is extended every lockExtendEvery while a run lasts: a process
	// that dies releases it within lockExpiry.
	lockExpiry      = 2 * time.Minute
	lockExtendEvery = 30 * time.Second
	// startDelay is the wait before the first run after the process starts.
	startDelay = time.Minute
)

type WorkerDeps struct {
	fx.In

	Config    *appconfig.Config
	DB        *bun.DB
	RedSync   *redsync.Redsync
	Lifecycle fx.Lifecycle
}

// NewArchiver returns the archiver the configuration describes, on db (the database of conf.PostgresDSN). It fails
// when the configuration is incomplete or the cold storage directory is unusable (missing, not writable, without a
// manifest that holds every month up to its last one).
func NewArchiver(ctx context.Context, conf *appconfig.Config, db *sql.DB) (*coldstore.Archiver, error) {
	if conf.ColdstoreS3Bucket == "" || conf.AWSAccessKey == "" || conf.AWSSecretKey == "" {
		return nil, errors.New("archivewkr: the archiving needs PENGUIN_V3_COLDSTORE_S3_BUCKET, PENGUIN_V3_AWS_ACCESS_KEY and PENGUIN_V3_AWS_SECRET_KEY")
	}
	bucket, err := coldstore.NewS3Bucket(ctx, coldstore.S3Options{
		Bucket:    conf.ColdstoreS3Bucket,
		Region:    conf.ColdstoreS3Region,
		Endpoint:  conf.ColdstoreS3Endpoint,
		AccessKey: conf.AWSAccessKey,
		SecretKey: conf.AWSSecretKey,
	})
	if err != nil {
		return nil, err
	}
	return coldstore.NewArchiver(coldstore.ArchiveOptions{
		Options: coldstore.Options{
			PostgresDSN:        conf.PostgresDSN,
			ExtensionDirectory: conf.ColdstoreExtensionDirectory,
			WorkDir:            conf.ColdstoreWorkDirectory,
			Threads:            conf.ColdstoreArchiveThreads,
			MemoryLimit:        conf.ColdstoreArchiveMemoryLimit,
		},
		Dir:          conf.ColdstoreDirectory,
		After:        conf.ColdstoreArchiveAfter,
		DropDelay:    conf.ColdstoreArchiveDropDelay,
		MinFreeBytes: conf.ColdstoreArchiveMinFreeBytes,
		Backup:       coldstore.BackupOptions{Prefix: conf.ColdstoreS3Prefix, StorageClass: conf.ColdstoreS3StorageClass},
	}, db, bucket)
}

// Start starts the archiving when it is enabled. An unusable configuration fails the start of the app.
func Start(deps WorkerDeps) error {
	conf := deps.Config
	if !conf.ColdstoreArchiveEnabled {
		log.Info().Str("evt.name", "worker.archivewkr.disabled").Msg("cold storage archiving is disabled due to configuration")
		return nil
	}
	if conf.ColdstoreArchiveInterval <= 0 {
		return errors.New("archivewkr: PENGUIN_V3_COLDSTORE_ARCHIVE_INTERVAL must be positive")
	}
	a, err := NewArchiver(context.Background(), conf, deps.DB.DB)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	deps.Lifecycle.Append(fx.Hook{
		OnStop: func(stopCtx context.Context) error {
			// a run in progress is abandoned: its transaction rolls back, and no partition is dropped
			cancel()
			select {
			case <-done:
			case <-stopCtx.Done():
			}
			return nil
		},
	})
	mutex := NewMutex(deps.RedSync)
	go func() {
		defer close(done)
		timer := time.NewTimer(startDelay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			_, _ = RunLocked(ctx, a, mutex)
			timer.Reset(conf.ColdstoreArchiveInterval)
		}
	}()
	log.Info().Str("evt.name", "worker.archivewkr.enabled").Str("dir", conf.ColdstoreDirectory).
		Dur("interval", conf.ColdstoreArchiveInterval).Msg("cold storage archiving is enabled")
	return nil
}

// NewMutex returns the lock of the archiving (LockName).
func NewMutex(rs *redsync.Redsync) *redsync.Mutex {
	return rs.NewMutex(LockName, redsync.WithExpiry(lockExpiry), redsync.WithTries(1))
}

// RunLocked runs the archiving once if it gets the lock, keeps the lock while it runs (the run is cancelled if the
// lock is lost, before any partition is dropped), and logs and counts the outcome.
func RunLocked(ctx context.Context, a *coldstore.Archiver, mutex *redsync.Mutex) (*coldstore.ArchiveResult, error) {
	logger := log.With().Str("evt.name", "worker.archivewkr").Logger()
	if err := mutex.LockContext(ctx); err != nil {
		observability.ColdstoreArchiveRuns.WithLabelValues("lock_not_acquired").Inc()
		logger.Info().Err(err).Msg("cold storage archiving: another process holds the lock")
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	extended := make(chan struct{})
	go func() {
		defer close(extended)
		ticker := time.NewTicker(lockExtendEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ok, err := mutex.ExtendContext(ctx); !ok || err != nil {
					logger.Error().Err(err).Msg("cold storage archiving: lost the lock, cancelling the run")
					cancel()
					return
				}
			}
		}
	}()
	started := time.Now()
	res, err := a.Run(ctx)
	cancel()
	<-extended
	if ok, uerr := mutex.Unlock(); !ok || uerr != nil {
		logger.Warn().Err(uerr).Msg("cold storage archiving: failed to release the lock")
	}

	outcome := coldstore.ArchiveOutcome(err)
	observability.ColdstoreArchiveRuns.WithLabelValues(outcome).Inc()
	ev := logger.Info()
	if err != nil {
		ev = logger.Error().Err(err)
	} else {
		observability.ColdstoreArchiveLastSuccess.SetToCurrentTime()
	}
	if res != nil {
		ev = ev.Strs("archived", res.Archived).Strs("pending", res.Pending).Strs("hot", res.Hot).Bool("partitionsCreated", res.PartitionsCreated)
	}
	ev.Str("outcome", outcome).Dur("took", time.Since(started)).Msg("cold storage archiving run finished")
	return res, err
}
