// Package duckdbx opens embedded DuckDB databases with the resource limits and extension settings the cold storage
// needs, so that every caller gets the same, offline configuration.
package duckdbx

import (
	"database/sql"
	"errors"
	"net/url"
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
	return sql.OpenDB(connector), nil
}

func (cfg Config) dsn() (string, error) {
	if strings.Contains(cfg.Path, "?") {
		return "", errors.New("duckdbx: database path must not contain '?'")
	}
	if cfg.Threads < 0 {
		return "", errors.New("duckdbx: threads must not be negative")
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
