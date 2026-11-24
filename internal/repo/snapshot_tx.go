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
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY"); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}
