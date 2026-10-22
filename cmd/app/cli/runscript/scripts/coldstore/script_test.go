package script_coldstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v2"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/coldstore"
)

// TestExportRefusals checks the refusals of coldstore-export that come before PostgreSQL is read: neither needs
// a database, and both must leave the directory untouched.
func TestExportRefusals(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, coldstore.ManifestFile)
	m := coldstore.NewManifest()
	m.PutMonth(coldstore.MonthEntry{Month: "2024-03", MonthStats: coldstore.MonthStats{Rows: 10}})
	if err := coldstore.WriteManifest(manifest, m); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	current := time.Now().UTC().Format("2006-01")

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--month", current}, "is not over"},
		{[]string{"--month", "2024-03"}, "already archived"},
		{[]string{"--month", "2024-03", "--allow-fewer-rows"}, "already archived"},
	}
	for _, c := range cases {
		app := &cli.App{
			Commands:  Commands(func() CommandDeps { return CommandDeps{Config: &appconfig.Config{}} }),
			Writer:    os.Stderr,
			ErrWriter: os.Stderr,
		}
		args := append([]string{"app", "coldstore-export", "--out", dir}, c.args...)
		err := app.RunContext(context.Background(), args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want %q", c.args, err, c.want)
		}
	}
	after, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("the manifest changed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		// the lock of the month, taken to check the manifest
		if e.Name() != coldstore.ManifestFile && e.Name() != coldstore.LocksDir {
			t.Errorf("unexpected %s in the directory", e.Name())
		}
	}
}
