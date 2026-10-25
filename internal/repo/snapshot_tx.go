package repo

import (
	"context"

	"github.com/uptrace/bun"
)

// RunInSnapshotTx runs fn in a read-only transaction at REPEATABLE READ: every statement of fn reads the same
// snapshot of the database, the one its first statement takes, and a write fails. fn returning an error rolls the
// transaction back. pgdriver rejects the isolation level and read only of sql.TxOptions (BeginTx fails), so they are
// set by the first statement of the transaction instead.
func RunInSnapshotTx(ctx context.Context, db *bun.DB, fn func(ctx context.Context, tx bun.Tx) error) error {
	return RunInLockedSnapshotTx(ctx, db, nil, fn)
}

// RunInLockedSnapshotTx runs fn as RunInSnapshotTx does, with tables locked in ACCESS SHARE mode until the
// transaction ends, from before the snapshot is taken (LOCK TABLE does not take it). TRUNCATE is not MVCC-safe: one
// committed after the snapshot was taken would empty a table for the snapshot too, while what else the TRUNCATE wrote
// (the day versions its trigger bumps) is not in it. Locked, the tables cannot be truncated (nor rewritten) while fn
// reads them, and a TRUNCATE running when the transaction starts is in the snapshot: the lock waits for it to commit.
func RunInLockedSnapshotTx(ctx context.Context, db *bun.DB, tables []string, fn func(ctx context.Context, tx bun.Tx) error) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY"); err != nil {
			return err
		}
		for _, table := range tables {
			if _, err := tx.ExecContext(ctx, "LOCK TABLE ? IN ACCESS SHARE MODE", bun.Ident(table)); err != nil {
				return err
			}
		}
		return fn(ctx, tx)
	})
}
