package script_coldstore

import (
	"context"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/coldstore"
)

func TestParsePatch(t *testing.T) {
	file := `
# reject rule 12 reevaluation
101 -1
102, 0   # comma-separated too
103 delete
`
	p, err := parsePatch([]string{"1", " 2"}, []string{"3=-1", "4 = 2"}, strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	want := coldstore.Patch{Delete: []int64{1, 2, 103}, Reliability: map[int64]int{3: -1, 4: 2, 101: -1, 102: 0}}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("patch = %+v, want %+v", p, want)
	}

	bad := []struct {
		deletes, reliabilities []string
		file, want             string
	}{
		{deletes: []string{"x"}, want: "invalid report id"},
		{reliabilities: []string{"3"}, want: "report_id=reliability"},
		{reliabilities: []string{"3=a"}, want: "report_id=reliability"},
		{reliabilities: []string{"3=1"}, file: "3 2", want: "line 1: report 3 is given a reliability twice"},
		{file: "\n5 1 2", want: "line 2"},
		{file: "5 maybe", want: "invalid reliability"},
		{file: "five 1", want: "invalid report id"},
	}
	for _, c := range bad {
		var r io.Reader
		if c.file != "" {
			r = strings.NewReader(c.file)
		}
		if _, err := parsePatch(c.deletes, c.reliabilities, r); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("parsePatch(%v, %v, %q) = %v, want %q", c.deletes, c.reliabilities, c.file, err, c.want)
		}
	}
}

// TestRecoverCommand runs coldstore-recover where there is nothing to recover (the recovery itself is tested in
// internal/coldstore): it succeeds without a database, and rejects an invalid month.
func TestRecoverCommand(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) error {
		app := &cli.App{
			Commands:  Commands(func() CommandDeps { return CommandDeps{Config: &appconfig.Config{}} }),
			Writer:    os.Stderr,
			ErrWriter: os.Stderr,
		}
		return app.RunContext(context.Background(), append([]string{"app", "coldstore-recover", "--dir", dir}, args...))
	}
	if err := run(); err != nil {
		t.Errorf("recover = %v", err)
	}
	if err := run("--month", "2026-09"); err != nil {
		t.Errorf("recover of a month without a journal = %v", err)
	}
	if err := run("--month", "2026-13"); err == nil {
		t.Error("recover of an invalid month succeeded")
	}
}
