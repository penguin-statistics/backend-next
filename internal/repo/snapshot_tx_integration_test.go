package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/driver/pgdriver"
)

// RunInSnapshotTx runs at REPEATABLE READ, read only: its statements all read the snapshot of the first one, and a
// write fails.
func TestRunInSnapshotTx(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	insert := `INSERT INTO pattern_matrix_elements (stage_id, pattern_id, quantity, times, server, day_num) VALUES (1, 1, 1, 1, 'CN', 100)`
	mustExec(t, db, insert)

	count := func(ctx context.Context, db bun.IDB) int {
		t.Helper()
		var n int
		if err := db.NewRaw("SELECT count(*) FROM pattern_matrix_elements").Scan(ctx, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	err := RunInSnapshotTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		for setting, want := range map[string]string{"transaction_isolation": "repeatable read", "transaction_read_only": "on"} {
			var got string
			if err := tx.NewRaw("SHOW "+setting).Scan(ctx, &got); err != nil {
				return err
			}
			if got != want {
				t.Errorf("%s = %q, want %q", setting, got, want)
			}
		}

		if n := count(ctx, tx); n != 1 {
			t.Fatalf("%d elements in the snapshot, want 1", n)
		}
		// committed after the snapshot was taken: not in it
		mustExec(t, db, insert)
		if n := count(ctx, tx); n != 1 {
			t.Fatalf("%d elements in the snapshot after a concurrent insert, want 1", n)
		}

		_, err := tx.ExecContext(ctx, insert)
		var pgErr pgdriver.Error
		if !errors.As(err, &pgErr) || pgErr.Field('C') != "25006" {
			t.Fatalf("a write in the snapshot transaction: %v, want read_only_sql_transaction (25006)", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(ctx, db); n != 2 {
		t.Fatalf("%d elements after the snapshot transaction, want 2", n)
	}

	// an error of fn is returned, after the rollback
	errFn := errors.New("fn failed")
	if err := RunInSnapshotTx(ctx, db, func(ctx context.Context, tx bun.Tx) error { return errFn }); !errors.Is(err, errFn) {
		t.Fatalf("RunInSnapshotTx = %v, want %v", err, errFn)
	}
}

// RunInLockedSnapshotTx locks the tables before the snapshot, which its first statement still takes, so a TRUNCATE
// waits for the transaction to end.
func TestRunInLockedSnapshotTx(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	insert := `INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, source_category, day_num) VALUES (1, 1, 1, 1, 'CN', 'all', 100)`
	mustExec(t, db, insert)

	count := func(ctx context.Context, db bun.IDB) int {
		t.Helper()
		var n int
		if err := db.NewRaw("SELECT count(*) FROM drop_matrix_elements").Scan(ctx, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// a TRUNCATE in a transaction of its own, giving up on the lock after lockTimeout
	truncate := func(lockTimeout string) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = '"+lockTimeout+"'"); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "TRUNCATE drop_matrix_elements")
			return err
		})
	}
	err := RunInLockedSnapshotTx(ctx, db, []string{"drop_matrix_elements"}, func(ctx context.Context, tx bun.Tx) error {
		var locked bool
		if err := db.NewRaw(`SELECT EXISTS (SELECT FROM pg_locks WHERE relation = 'drop_matrix_elements'::regclass
				AND mode = 'AccessShareLock' AND granted AND pid <> pg_backend_pid())`).Scan(ctx, &locked); err != nil {
			return err
		}
		if !locked {
			t.Error("drop_matrix_elements is not locked in ACCESS SHARE mode")
		}

		// committed after the lock, before the first statement: in the snapshot
		mustExec(t, db, insert)
		if n := count(ctx, tx); n != 2 {
			t.Fatalf("%d elements in the snapshot, want 2: the lock took the snapshot", n)
		}
		mustExec(t, db, insert)
		if n := count(ctx, tx); n != 2 {
			t.Fatalf("%d elements in the snapshot after a concurrent insert, want 2", n)
		}

		err := truncate("200ms")
		var pgErr pgdriver.Error
		if !errors.As(err, &pgErr) || pgErr.Field('C') != "55P03" {
			t.Fatalf("a TRUNCATE during the snapshot transaction: %v, want lock_not_available (55P03)", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// the lock is released with the transaction
	if err := truncate("10s"); err != nil {
		t.Fatalf("a TRUNCATE after the snapshot transaction: %v", err)
	}
}
