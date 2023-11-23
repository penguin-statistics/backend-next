package repo

import (
	"context"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type RecognitionDefect struct {
	db  *bun.DB
	sel selector.S[model.RecognitionDefect]
}

func NewRecognitionDefect(db *bun.DB) *RecognitionDefect {
	return &RecognitionDefect{db: db, sel: selector.New[model.RecognitionDefect](db)}
}

func (r *RecognitionDefect) CreateDefectReportDraft(ctx context.Context, defectReport *model.RecognitionDefect) error {
	defectReport.DefectID = strings.ToLower(ulid.Make().String())

	_, err := r.db.NewInsert().
		Model(defectReport).
		Exec(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (r *RecognitionDefect) FinalizeDefectReport(ctx context.Context, defectId, imageUri string) error {
	_, err := r.db.NewUpdate().
		Model((*model.RecognitionDefect)(nil)).
		Set("image_uri = ?", imageUri).
		Set("updated_at = ?", time.Now()).
		Where("defect_id = ?", defectId).
		Exec(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (r *RecognitionDefect) GetDefectReports(ctx context.Context, limit int, page int) ([]*model.RecognitionDefect, error) {
	return r.sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Order("created_at DESC").Limit(limit).Offset(page * limit)
	})
}

func (r *RecognitionDefect) GetDefectReport(ctx context.Context, defectId string) (*model.RecognitionDefect, error) {
	return r.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("defect_id = ?", defectId)
	})
}
