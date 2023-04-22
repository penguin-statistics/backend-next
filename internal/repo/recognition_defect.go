package repo

import (
	"context"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
)

type RecognitionDefect struct {
	DB *bun.DB
}

func NewRecognitionDefect(db *bun.DB) *RecognitionDefect {
	return &RecognitionDefect{DB: db}
}

func (s *RecognitionDefect) CreateDefectReportDraft(ctx context.Context, defectReport *model.RecognitionDefect) error {
	defectReport.DefectID = strings.ToLower(ulid.Make().String())

	_, err := s.DB.NewInsert().
		Model(defectReport).
		Exec(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (s *RecognitionDefect) FinalizeDefectReport(ctx context.Context, defectId, imageUri string) error {
	_, err := s.DB.NewUpdate().
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

func (s *RecognitionDefect) GetDefectReports(ctx context.Context, limit int, page int) ([]*model.RecognitionDefect, error) {
	var defectReports []*model.RecognitionDefect

	query := s.DB.NewSelect().
		Model(&defectReports).
		Order("created_at DESC").
		Limit(limit).
		Offset(page * limit)

	err := query.Scan(ctx)
	if err != nil {
		return nil, err
	}

	return defectReports, nil
}

func (s *RecognitionDefect) GetDefectReport(ctx context.Context, defectId string) (*model.RecognitionDefect, error) {
	var defectReport model.RecognitionDefect

	err := s.DB.NewSelect().
		Model(&defectReport).
		Where("defect_id = ?", defectId).
		Scan(ctx)
	if err != nil {
		return nil, err
	}

	return &defectReport, nil
}
