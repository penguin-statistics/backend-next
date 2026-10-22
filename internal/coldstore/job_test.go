package coldstore

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
)

// TestAttachErrorRedactsPassword makes DuckDB's postgres extension fail to connect with passwords that are encoded
// differently in a URI's userinfo and in a query (a space is %20 in one and + in the other), and checks that no form
// of the password is left in the error. DuckDB echoes the whole connection URI in the error. Needs only the
// extension (PENGUIN_TEST_DUCKDB_EXTENSION_DIR): nothing listens on port 1.
func TestAttachErrorRedactsPassword(t *testing.T) {
	extDir := os.Getenv("PENGUIN_TEST_DUCKDB_EXTENSION_DIR")
	if extDir == "" {
		t.Skip("PENGUIN_TEST_DUCKDB_EXTENSION_DIR is not set")
	}
	for _, p := range testPasswords {
		dsn := "postgres://runner:" + strings.ReplaceAll(url.QueryEscape(p), "+", "%20") +
			"@127.0.0.1:1/postgres?sslmode=disable&connect_timeout=1"
		opts := Options{PostgresDSN: dsn, ExtensionDirectory: extDir, WorkDir: t.TempDir(), Threads: 1, MemoryLimit: "256MB"}
		j, err := newJob(context.Background(), opts, "redact", true)
		if err == nil {
			j.Close()
			t.Fatalf("password %q: connected to port 1", p)
		}
		msg := err.Error()
		if !strings.Contains(msg, "attach PostgreSQL") {
			t.Fatalf("password %q: err = %v, want an ATTACH error", p, err)
		}
		for _, f := range passwordForms(p) {
			if strings.Contains(msg, f) {
				t.Errorf("password %q: %q left in %s", p, f, msg)
			}
		}
	}
}
