package coldstore_test

// The recall test of the monthly archiving runs the app's recall query (internal/repo), so it is an external test:
// internal/repo imports internal/coldstore. It uses the helpers of archive_integration_test.go through
// export_test.go.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"exusiai.dev/backend-next/internal/coldstore"
	"exusiai.dev/backend-next/internal/repo"
)

// A recall (repo.DropReport.DeleteDropReport) does not lock the partitions of an archived month: it does not wait
// for the archiving that holds them locked, and the archiving does not wait for it.
func TestArchiveDropDoesNotWaitForRecall(t *testing.T) {
	ctx := context.Background()
	d := coldstore.NewTestData()
	opts, db := coldstore.NewTestNewDB(t, d, "2026-08", "2026-09")
	b := coldstore.NewFakeBucket()
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	a := coldstore.NewTestArchiver(t, opts, db, b, &now, "2026-07")
	// the recall dates reports by the wall clock: a report of now, in a partition of its own
	if _, err := coldstore.CreatePartitions(ctx, db, time.Now(), time.Second); err != nil {
		t.Fatal(err)
	}
	const id = 1 << 30
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO drop_reports (report_id, stage_id, pattern_id, times, reliability, server,
		account_id) VALUES (%d, 1, 1, 1, 0, 'CN', 1)`, id)); err != nil {
		t.Fatal(err)
	}
	if res, err := a.Run(ctx); err != nil || strings.Join(res.Pending, " ") != "2026-08" {
		t.Fatalf("run that records the month = %+v, %v", res, err)
	}
	now = now.Add(coldstore.DefaultDropDelay)

	bdb := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(opts.PostgresDSN))), pgdialect.New())
	t.Cleanup(func() { bdb.Close() })
	tries := 0
	coldstore.SetArchiveHook(t, func(month, step string) error {
		if step != "verified" {
			return nil
		}
		if tries++; tries > 1 {
			return nil
		}
		// the archiving holds the month's partitions locked
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		n, err := repo.NewDropReport(bdb).DeleteDropReport(ctx, id)
		if err != nil || n != 1 {
			return fmt.Errorf("recall = %d, %v", n, err)
		}
		return nil
	})
	res, err := a.Run(ctx)
	if err != nil || strings.Join(res.Archived, " ") != "2026-08" {
		t.Fatalf("run = %+v, %v", res, err)
	}
	if tries != 1 {
		t.Errorf("the month was verified %d times, want once", tries)
	}
	var reliability int
	if err := db.QueryRowContext(ctx, `SELECT reliability FROM drop_reports WHERE report_id = $1`, id).Scan(&reliability); err != nil {
		t.Fatal(err)
	}
	if reliability != -1 {
		t.Errorf("reliability of the recalled report = %d, want -1", reliability)
	}
}
