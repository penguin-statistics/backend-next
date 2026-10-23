package coldstore

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	"exusiai.dev/backend-next/internal/pkg/duckdbx"
)

// Options configures the DuckDB databases that export, verify and index the cold storage.
type Options struct {
	// PostgresDSN is the DSN of the PostgreSQL database holding the reports, in the app's format (see
	// appconfig.ConfigSpec.PostgresDSN); it is converted for DuckDB's postgres extension. Only needed to export
	// and to verify against the source.
	PostgresDSN string
	// ExtensionDirectory is where DuckDB's postgres extension is preinstalled (duckdbx.ImageExtensionDirectory in
	// the image).
	ExtensionDirectory string
	// WorkDir is where every job creates a directory of its own for its database file and spill files, removed when
	// the job ends. Empty is the system's temp directory. A month's export holds the month's rows (several GB for
	// the largest months).
	WorkDir string
	// Threads caps DuckDB's threads per job; 0 is one per core.
	Threads int
	// MemoryLimit caps DuckDB's memory per job (e.g. "4GB"); empty is DuckDB's default (80% of the RAM).
	MemoryLimit string
}

// job is one DuckDB database used by a single export, verification or index run. Every job has a database file
// and a temp directory of its own: concurrent DuckDB processes that shared a temp directory silently corrupted
// their output, and an in-memory database runs out of memory on the large months.
type job struct {
	dir  string
	db   *sql.DB
	conn *sql.Conn
	// dsns are the PostgreSQL DSNs (the configured one and the libpq one given to DuckDB) whose passwords are
	// redacted from every error of the job: DuckDB's postgres extension opens new connections after the ATTACH
	// (e.g. after the server restarted, or to scan in parallel), and an error to connect echoes the whole DSN.
	dsns []string
	// orphanExtras counts the rows of the month's drop_report_extras partition that belong to none of the month's
	// reports, on the schema of the new database (see countOrphanExtras); -1 otherwise.
	orphanExtras int64
}

func newJob(ctx context.Context, opts Options, name string, attachPostgres bool) (_ *job, err error) {
	if opts.WorkDir != "" {
		if err := os.MkdirAll(opts.WorkDir, 0o755); err != nil {
			return nil, errors.Wrap(err, "coldstore: create work directory")
		}
	}
	dir, err := os.MkdirTemp(opts.WorkDir, "coldstore-"+name+"-")
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: create job directory")
	}
	j := &job{dir: dir}
	defer func() {
		if err != nil {
			j.Close()
		}
	}()

	j.db, err = duckdbx.Open(duckdbx.Config{
		Path:               filepath.Join(dir, "job.duckdb"),
		Threads:            opts.Threads,
		MemoryLimit:        opts.MemoryLimit,
		TempDirectory:      filepath.Join(dir, "tmp"),
		ExtensionDirectory: opts.ExtensionDirectory,
	})
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: open duckdb")
	}
	// settings such as TimeZone are per connection: run the whole job on one
	j.conn, err = j.db.Conn(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: connect duckdb")
	}
	// the rollup's hours are UTC hours
	if err = j.exec(ctx, "SET TimeZone = 'UTC'"); err != nil {
		return nil, err
	}

	if attachPostgres {
		if opts.PostgresDSN == "" {
			return nil, errors.New("coldstore: no PostgreSQL DSN")
		}
		dsn, err := libpqDSN(opts.PostgresDSN)
		if err != nil {
			return nil, err
		}
		j.dsns = []string{dsn, opts.PostgresDSN}
		if err = j.exec(ctx, "LOAD postgres"); err != nil {
			return nil, err
		}
		if _, err = j.conn.ExecContext(ctx, "ATTACH "+quote(dsn)+" AS pg (TYPE postgres, READ_ONLY)"); err != nil {
			// the DSN holds the password: do not echo the statement
			return nil, j.fail(err, "attach PostgreSQL")
		}
	}
	return j, nil
}

func (j *job) exec(ctx context.Context, query string) error {
	if _, err := j.conn.ExecContext(ctx, query); err != nil {
		return j.fail(err, firstLine(query))
	}
	return nil
}

func (j *job) queryRow(ctx context.Context, query string, dest ...any) error {
	if err := j.conn.QueryRowContext(ctx, query).Scan(dest...); err != nil {
		return j.fail(err, firstLine(query))
	}
	return nil
}

// query runs a query on the job's connection and calls scan for every row; scan's own errors are returned as they
// are, the database's go through fail.
func (j *job) query(ctx context.Context, query string, scan func(*sql.Rows) error) error {
	rows, err := j.conn.QueryContext(ctx, query)
	if err != nil {
		return j.fail(err, firstLine(query))
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return j.fail(err, firstLine(query))
		}
	}
	if err := rows.Err(); err != nil {
		return j.fail(err, firstLine(query))
	}
	return nil
}

// fail returns err of the job's database, prefixed with what, with the passwords of the job's DSNs redacted. Every
// error of the job's connection goes through it.
func (j *job) fail(err error, what string) error {
	if len(j.dsns) == 0 {
		return errors.Wrapf(err, "coldstore: %s", what)
	}
	return &redactedError{msg: "coldstore: " + what + ": " + redactDSN(err.Error(), j.dsns...), cause: err}
}

// redactedError is an error whose message has a password redacted. It does not unwrap to its cause, whose message
// holds the password; errors.Is still sees through it (e.g. context.Canceled).
type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Is(target error) bool { return errors.Is(e.cause, target) }

// Close closes the database and removes the job directory.
func (j *job) Close() {
	if j.conn != nil {
		_ = j.conn.Close()
	}
	if j.db != nil {
		_ = j.db.Close()
	}
	_ = os.RemoveAll(j.dir)
}

// quote returns s as a SQL string literal.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func firstLine(query string) string {
	query = strings.TrimSpace(query)
	if i := strings.IndexByte(query, '\n'); i >= 0 {
		query = query[:i] + " ..."
	}
	return query
}

// redactDSN removes the passwords of the DSNs from msg. DuckDB echoes the connection URI it was given, as it was
// given (e.g. "two%20words", which url.QueryEscape writes "two+words"), and libpq may echo it decoded, so every
// form the password can take is removed: decoded, as it appears in each DSN's userinfo, as Go's userinfo, path and
// query escapes write it; and then the userinfo password of any postgres:// URI left in msg.
func redactDSN(msg string, dsns ...string) string {
	var forms []string
	for _, dsn := range dsns {
		u, err := url.Parse(dsn)
		if err != nil || u.User == nil {
			continue
		}
		p, ok := u.User.Password()
		if !ok || p == "" {
			continue
		}
		q := url.QueryEscape(p)
		forms = append(forms, p, q, strings.ReplaceAll(q, "+", "%20"), url.PathEscape(p), rawPassword(dsn))
		if _, enc, ok := strings.Cut(u.User.String(), ":"); ok {
			forms = append(forms, enc)
		}
	}
	// longest first, so that a form is not cut by a shorter one it contains
	sort.Slice(forms, func(i, k int) bool { return len(forms[i]) > len(forms[k]) })
	for _, f := range forms {
		if f != "" {
			msg = strings.ReplaceAll(msg, f, "***")
		}
	}
	return uriPassword.ReplaceAllString(msg, "${1}***@")
}

// uriPassword matches the userinfo password of a postgres:// URI.
var uriPassword = regexp.MustCompile(`(?i)(postgres(?:ql)?://[^:@/\s]*:)[^@\s]*@`)

// rawPassword returns the password of a URI's userinfo exactly as it is written in the URI.
func rawPassword(dsn string) string {
	_, rest, ok := strings.Cut(dsn, "://")
	if !ok {
		return ""
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	at := strings.LastIndexByte(rest, '@')
	if at < 0 {
		return ""
	}
	_, p, _ := strings.Cut(rest[:at], ":")
	return p
}

// libpqDSN converts a DSN of the app's PostgreSQL driver (bun's pgdriver) to a libpq connection URI for DuckDB's
// postgres extension: the driver's timeouts are not libpq parameters, and the driver sends any parameter it does
// not know (e.g. search_path) as a run-time setting, which libpq takes in options.
func libpqDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", errors.New("coldstore: invalid PostgreSQL DSN")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", fmt.Errorf("coldstore: unsupported PostgreSQL DSN scheme %q", u.Scheme)
	}

	in := u.Query()
	out := url.Values{}
	var settings []string
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := in.Get(k)
		switch k {
		case "sslmode", "sslrootcert", "application_name", "host":
			out.Set(k, v)
		case "connect_timeout", "dial_timeout":
			d, err := time.ParseDuration(v)
			if err != nil {
				// pgdriver also accepts a plain number of seconds
				s, err := strconv.Atoi(v)
				if err != nil {
					return "", fmt.Errorf("coldstore: invalid %s %q in PostgreSQL DSN", k, v)
				}
				d = time.Duration(s) * time.Second
			}
			out.Set("connect_timeout", strconv.Itoa(int(math.Ceil(d.Seconds()))))
		case "timeout", "read_timeout", "write_timeout":
			// pgdriver's socket timeouts: an export reads for a long time, leave them out
		default:
			settings = append(settings, "-c "+k+"="+strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), " ", `\ `))
		}
	}
	if len(settings) > 0 {
		out.Set("options", strings.Join(settings, " "))
	}
	// libpq does not decode '+' as a space in a URI
	u.RawQuery = strings.ReplaceAll(out.Encode(), "+", "%20")
	return u.String(), nil
}
