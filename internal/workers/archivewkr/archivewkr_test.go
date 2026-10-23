package archivewkr

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"go.uber.org/fx/fxtest"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/coldstore"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

// writeTestManifest writes the manifest of a cold storage directory that holds the given months (none of their
// files, which only the runs read).
func writeTestManifest(t *testing.T, dir string, months ...string) {
	t.Helper()
	m := coldstore.NewManifest()
	for _, key := range months {
		e := coldstore.MonthEntry{Month: key}
		for _, kind := range coldstore.Kinds {
			e.Files = append(e.Files, coldstore.FileEntry{Path: coldstore.FilePath(kind, key)})
		}
		m.PutMonth(e)
	}
	m.PutDim(coldstore.FileEntry{Path: coldstore.DropPatternElementsFile})
	b, err := coldstore.MarshalManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, coldstore.ManifestFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func testConfig(t *testing.T) *appconfig.Config {
	t.Helper()
	dir := t.TempDir()
	writeTestManifest(t, dir, coldstore.PreMonth, "2019-05")
	return &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{
		PostgresDSN:                 "postgres://127.0.0.1:1/unused?sslmode=disable",
		ColdstoreArchiveEnabled:     true,
		ColdstoreDirectory:          dir,
		ColdstoreWorkDirectory:      t.TempDir(),
		ColdstoreArchiveAfter:       60 * 24 * time.Hour,
		ColdstoreArchiveInterval:    time.Hour,
		ColdstoreS3Bucket:           "bucket",
		ColdstoreS3Region:           "ap-southeast-1",
		ColdstoreS3Prefix:           "coldstore/v1",
		ColdstoreS3StorageClass:     "STANDARD_IA",
		ColdstoreExtensionDirectory: t.TempDir(),
		AWSAccessKey:                "key",
		AWSSecretKey:                "secret",
	}}
}

func unusedDB(t *testing.T, dsn string) *bun.DB {
	t.Helper()
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn))), pgdialect.New())
	t.Cleanup(func() { db.Close() })
	return db
}

func TestStartDisabled(t *testing.T) {
	conf := testConfig(t)
	conf.ColdstoreArchiveEnabled = false
	// nothing is used: no database, Redis or lifecycle
	if err := Start(WorkerDeps{Config: conf}); err != nil {
		t.Fatal(err)
	}
}

// A configuration the archiving cannot run with fails the start of the app instead of failing every run.
func TestStartRefusesAnUnusableConfiguration(t *testing.T) {
	for name, change := range map[string]func(c *appconfig.Config){
		"no bucket":         func(c *appconfig.Config) { c.ColdstoreS3Bucket = "" },
		"no keys":           func(c *appconfig.Config) { c.AWSSecretKey = "" },
		"no directory":      func(c *appconfig.Config) { c.ColdstoreDirectory = "" },
		"missing directory": func(c *appconfig.Config) { c.ColdstoreDirectory = filepath.Join(c.ColdstoreDirectory, "missing") },
		"corrupt manifest": func(c *appconfig.Config) {
			if err := os.WriteFile(filepath.Join(c.ColdstoreDirectory, "manifest.json"), []byte("{"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		// e.g. a mount point without its disk: the archiving would replace the bucket's manifest with one month
		"no manifest": func(c *appconfig.Config) {
			if err := os.Remove(filepath.Join(c.ColdstoreDirectory, "manifest.json")); err != nil {
				t.Fatal(err)
			}
		},
		"manifest with a gap": func(c *appconfig.Config) { writeTestManifest(t, c.ColdstoreDirectory, coldstore.PreMonth, "2019-06") },
		"archiving too early": func(c *appconfig.Config) { c.ColdstoreArchiveAfter = 24 * time.Hour },
		"no interval":         func(c *appconfig.Config) { c.ColdstoreArchiveInterval = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			conf := testConfig(t)
			change(conf)
			lc := fxtest.NewLifecycle(t)
			if err := Start(WorkerDeps{Config: conf, DB: unusedDB(t, conf.PostgresDSN), Lifecycle: lc}); err == nil {
				t.Error("started")
			}
		})
	}
}

// newTestRedSync connects to PENGUIN_TEST_REDIS_URL, a disposable Redis (see calcwkr's lock tests).
func newTestRedSync(t *testing.T) *redsync.Redsync {
	t.Helper()
	url := os.Getenv("PENGUIN_TEST_REDIS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_REDIS_URL is not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { client.Close() })
	return redsync.New(goredis.NewPool(client))
}

func TestRunLocked(t *testing.T) {
	rs := newTestRedSync(t)
	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PENGUIN_TEST_POSTGRES_DSN is not set")
	}
	conf := testConfig(t)
	conf.PostgresDSN = dsn
	db := unusedDB(t, dsn)
	a, err := NewArchiver(context.Background(), conf, db.DB)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("%s:test:%d", LockName, time.Now().UnixNano())
	newMutex := func() *redsync.Mutex {
		return rs.NewMutex(name, redsync.WithExpiry(10*time.Second), redsync.WithTries(1))
	}

	// another process archives: nothing runs
	other := newMutex()
	if err := other.Lock(); err != nil {
		t.Fatal(err)
	}
	busy := testutil.ToFloat64(observability.ColdstoreArchiveRuns.WithLabelValues("lock_not_acquired"))
	if _, err := RunLocked(context.Background(), a, newMutex()); err == nil {
		t.Error("ran without the lock")
	}
	if got := testutil.ToFloat64(observability.ColdstoreArchiveRuns.WithLabelValues("lock_not_acquired")); got != busy+1 {
		t.Errorf("lock_not_acquired runs = %v, want %v", got, busy+1)
	}
	if _, err := other.Unlock(); err != nil {
		t.Fatal(err)
	}

	// a database without partitions has nothing to archive; the lock is released afterwards
	ok := testutil.ToFloat64(observability.ColdstoreArchiveRuns.WithLabelValues("ok"))
	res, err := RunLocked(context.Background(), a, newMutex())
	if err != nil || res.PartitionsCreated || len(res.Archived) != 0 || len(res.Hot) != 0 {
		t.Fatalf("run = %+v, %v", res, err)
	}
	if got := testutil.ToFloat64(observability.ColdstoreArchiveRuns.WithLabelValues("ok")); got != ok+1 {
		t.Errorf("ok runs = %v, want %v", got, ok+1)
	}
	if err := newMutex().Lock(); err != nil {
		t.Errorf("lock after the run: %v", err)
	}
}
