// Package duckdbx opens embedded DuckDB databases with the resource limits and extension settings the cold storage
// needs, so that every caller gets the same, offline configuration.
package duckdbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/duckdb/duckdb-go/v2"
)

// ImageExtensionDirectory is where the runtime image preinstalls the DuckDB extensions the app loads (see the
// Dockerfile). Extensions live under <dir>/<duckdb version>/<platform>/.
const ImageExtensionDirectory = "/opt/duckdb/extensions"

type Config struct {
	// Path is the database file. Empty opens an in-memory database. Jobs that may spill (exports of a whole month)
	// must use a file of their own: DuckDB caches file-backed instances by path, and an in-memory database runs
	// out of memory on large inputs.
	Path string
	// Threads caps the worker threads of the database; 0 keeps DuckDB's default (one per core).
	Threads int
	// MemoryLimit caps the memory of the database, in DuckDB's syntax (e.g. "1GB"); empty keeps DuckDB's default
	// (80% of the RAM).
	MemoryLimit string
	// TempDirectory is where the database spills. Concurrent databases must not share one: DuckDB processes that
	// shared a temp directory have been seen to silently corrupt their output.
	TempDirectory string
	// ExtensionDirectory is where LOAD looks for extensions that are not built in, such as postgres; empty keeps
	// DuckDB's default (~/.duckdb/extensions).
	ExtensionDirectory string

	// TimeZone, if set, is the database's time zone (e.g. "UTC"), which date_trunc and the casts of TIMESTAMPTZ use.
	TimeZone string
	// ParquetMetadataCache keeps the footers of the Parquet files read in memory, for a long-lived database that
	// reads the same files again and again. DuckDB checks a cached footer against the file before using it, so a
	// file replaced by a rename is read again.
	ParquetMetadataCache bool
	// AllowedDirectories, if not empty, confines the database to the local files under these directories and
	// TempDirectory: enable_external_access is turned off, so every other path, every remote file system (s3://,
	// http://, ...), and LOAD and INSTALL fail, and then the configuration is locked, so that no query can undo any
	// of this.
	AllowedDirectories []string
}

// Open opens a DuckDB database. Extensions are never installed or loaded behind the caller's back: anything beyond
// the statically linked ones (parquet, json, icu) has to be LOADed explicitly from ExtensionDirectory, so the app
// never reaches out to the network for them. Closing the returned DB closes the database.
func Open(cfg Config) (*sql.DB, error) {
	dsn, err := cfg.dsn()
	if err != nil {
		return nil, err
	}
	connector, err := duckdb.NewConnector(dsn, nil)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	if stmts := cfg.globalSettings(); len(stmts) > 0 {
		if err := applyGlobal(db, stmts); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

// globalSettings returns the statements that complete the configuration of the DSN: the settings of extensions,
// which DuckDB does not take before they are loaded, and the lockdown, whose directories are a list, which the DSN
// cannot carry. They are global, so they hold for every connection of the pool.
func (cfg Config) globalSettings() []string {
	var stmts []string
	if cfg.TimeZone != "" {
		stmts = append(stmts, "SET GLOBAL TimeZone = "+quote(cfg.TimeZone))
	}
	if cfg.ParquetMetadataCache {
		stmts = append(stmts, "SET GLOBAL parquet_metadata_cache = true")
	}
	if len(cfg.AllowedDirectories) > 0 {
		dirs := append([]string{}, cfg.AllowedDirectories...)
		if cfg.TempDirectory != "" {
			dirs = append(dirs, cfg.TempDirectory)
		}
		quoted := make([]string, len(dirs))
		for i, d := range dirs {
			// a prefix of paths: without the separator, /data/cs would also allow /data/cs-other
			quoted[i] = quote(strings.TrimSuffix(filepath.Clean(d), string(filepath.Separator)) + string(filepath.Separator))
		}
		stmts = append(stmts,
			"SET GLOBAL allowed_directories = ["+strings.Join(quoted, ", ")+"]",
			"SET GLOBAL enable_external_access = false",
			"SET GLOBAL lock_configuration = true",
		)
	}
	return stmts
}

func applyGlobal(db *sql.DB, stmts []string) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("duckdbx: %s: %w", stmt, err)
		}
	}
	return nil
}

// quote returns s as a SQL string literal.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func (cfg Config) dsn() (string, error) {
	if strings.Contains(cfg.Path, "?") {
		return "", errors.New("duckdbx: database path must not contain '?'")
	}
	if cfg.Threads < 0 {
		return "", errors.New("duckdbx: threads must not be negative")
	}
	for _, d := range cfg.AllowedDirectories {
		if !filepath.IsAbs(d) {
			return "", fmt.Errorf("duckdbx: allowed directory %q is not absolute", d)
		}
	}
	if len(cfg.AllowedDirectories) > 0 && cfg.TempDirectory != "" && !filepath.IsAbs(cfg.TempDirectory) {
		return "", fmt.Errorf("duckdbx: temp directory %q is not absolute", cfg.TempDirectory)
	}

	opts := url.Values{}
	opts.Set("autoinstall_known_extensions", "false")
	opts.Set("autoload_known_extensions", "false")
	if cfg.Threads > 0 {
		opts.Set("threads", strconv.Itoa(cfg.Threads))
	}
	if cfg.MemoryLimit != "" {
		opts.Set("memory_limit", cfg.MemoryLimit)
	}
	if cfg.TempDirectory != "" {
		opts.Set("temp_directory", cfg.TempDirectory)
	}
	if cfg.ExtensionDirectory != "" {
		opts.Set("extension_directory", cfg.ExtensionDirectory)
	}
	return cfg.Path + "?" + opts.Encode(), nil
}
