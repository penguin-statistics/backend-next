package repo

import (
	"context"
	"strconv"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

// replaceElementsOfDay replaces the elements of server on dayNum in table (that of the model E) by elements in a
// single transaction: readers see either the elements before or those after, never a day without elements, and a
// failure leaves the elements before. The replacements of a day are serialized by an advisory lock, taken before
// the delete: without it, two of them running at once both delete only the elements before, and the day ends up
// with the elements of both. dayOf gives the server and day of an element; an element of another day would not be
// replaced by the next replacement of its day, so it is an error.
func replaceElementsOfDay[E any](
	ctx context.Context, db *bun.DB, table string, server string, dayNum int, elements []*E, dayOf func(*E) (string, int),
) error {
	for _, element := range elements {
		if elementServer, elementDayNum := dayOf(element); elementServer != server || elementDayNum != dayNum {
			return errors.Errorf("replacing the %s of %s on day %d with an element of %s on day %d",
				table, server, dayNum, elementServer, elementDayNum)
		}
	}
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?), hashtext(?))",
			table, server+":"+strconv.Itoa(dayNum)); err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model((*E)(nil)).Where("server = ?", server).Where("day_num = ?", dayNum).Exec(ctx); err != nil {
			return err
		}
		if len(elements) == 0 {
			return nil
		}
		_, err := tx.NewInsert().Model(&elements).Exec(ctx)
		return err
	})
}
