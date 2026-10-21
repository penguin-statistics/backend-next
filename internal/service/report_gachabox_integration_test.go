package service

// Integration test of the gachabox aggregation of a report on a real PostgreSQL, skipped unless
// PENGUIN_TEST_POSTGRES_DSN is set, see internal/repo/integration_test.go.

import (
	"context"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/repo"
)

func TestPipelineAggregateGachaboxDrops(t *testing.T) {
	db := newTestDB(t)
	flushCaches(t)
	s := &Report{StageService: NewStage(repo.NewStage(db))}
	ctx := context.Background()

	mustExec(t, db, "INSERT INTO stages (ark_stage_id, zone_id, stage_type, extra_process_type) VALUES ('box', 1, 'ACTIVITY', ?), ('main', 1, 'MAIN', NULL)",
		constant.ExtraProcessTypeGachaBox)

	aggregate := func(arkStageId string) (times int, err error) {
		report := &types.ReportTaskSingleReport{
			FragmentStageID: types.FragmentStageID{StageID: arkStageId},
			Drops:           []*types.Drop{{ItemID: 1, Quantity: 3}, {ItemID: 2, Quantity: 4}},
			Times:           1,
		}
		err = s.PipelineAggregateGachaboxDrops(ctx, report)
		return report.Times, err
	}

	// a gachabox report counts a time per item drawn
	if times, err := aggregate("box"); err != nil || times != 7 {
		t.Errorf("gachabox stage: times = %d, err = %v; want 7, nil", times, err)
	}
	// and its stage is then looked up in the cache, not the database
	mustExec(t, db, "UPDATE stages SET extra_process_type = NULL WHERE ark_stage_id = 'box'")
	if times, err := aggregate("box"); err != nil || times != 7 {
		t.Errorf("gachabox stage from the cache: times = %d, err = %v; want 7, nil", times, err)
	}
	if times, err := aggregate("main"); err != nil || times != 1 {
		t.Errorf("other stage: times = %d, err = %v; want 1, nil", times, err)
	}

	// an unknown stage is not found, and is found once created
	if _, err := aggregate("new"); !errors.Is(err, pgerr.ErrNotFound) {
		t.Errorf("unknown stage: got %v, want pgerr.ErrNotFound", err)
	}
	mustExec(t, db, "INSERT INTO stages (ark_stage_id, zone_id, stage_type, extra_process_type) VALUES ('new', 1, 'ACTIVITY', ?)",
		constant.ExtraProcessTypeGachaBox)
	if times, err := aggregate("new"); err != nil || times != 7 {
		t.Errorf("stage created after a miss: times = %d, err = %v; want 7, nil", times, err)
	}
}
