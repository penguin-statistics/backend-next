package repo

import (
	"context"
	"testing"
)

// SelectMany scans into a slice, for which bun never returns sql.ErrNoRows: no rows is a nil slice and no error.
func TestSelectManyWithoutRowsIsNotAnError(t *testing.T) {
	db := newTestDB(t)
	elements, err := NewDropPatternElement(db).GetDropPatternElementsByPatternId(context.Background(), 1)
	if err != nil {
		t.Fatalf("err %v, want none", err)
	}
	if elements != nil {
		t.Fatalf("elements %v, want a nil slice", elements)
	}
}
