package reportwkr

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model/types"
)

// cancelOnInsert cancels the task's context at the first INSERT of persist, and waits for database/sql to roll back
// the transaction as the context ended, before the INSERT runs and fails.
type cancelOnInsert struct {
	cancel context.CancelFunc
	once   sync.Once
}

func (h *cancelOnInsert) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if strings.HasPrefix(event.Query, "INSERT") {
		h.once.Do(func() {
			h.cancel()
			// database/sql rolls the transaction back from its own goroutine once the context is done
			time.Sleep(200 * time.Millisecond)
		})
	}
	return ctx
}

func (h *cancelOnInsert) AfterQuery(context.Context, *bun.QueryEvent) {}

// TestProcessDeadlineIsNoFailedRollback ends the task's context while persist runs, as its deadline does: database/sql
// rolls the transaction back itself, so the explicit rollback finds it done. process fails and stores nothing, and
// logs no failed rollback.
func TestProcessDeadlineIsNoFailedRollback(t *testing.T) {
	w := newTestWorker(t)
	var logs bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&logs)
	t.Cleanup(func() { log.Logger = prev })

	task := &types.ReportTask{}
	if err := json.Unmarshal(newTestTask(t, w, 42, time.UnixMicro(1760600000123456), "203.0.113.7",
		&types.ReportTaskSingleReport{
			FragmentStageID: types.FragmentStageID{StageID: "main_01-07"},
			Drops:           []*types.Drop{{DropType: "REGULAR", ItemID: 30012, Quantity: 2}},
			Times:           1,
		}), task); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.DB.AddQueryHook(&cancelOnInsert{cancel: cancel})

	if err := w.process(ctx, task); err == nil {
		t.Fatal("process succeeded, want the error of the ended context")
	}
	assertRows(t, w.DB, dumpDropReports, []string{})
	if strings.Contains(logs.String(), "failed to rollback transaction") {
		t.Errorf("a failed rollback was logged:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "rolling back transaction due to error") {
		t.Errorf("the rollback was not logged:\n%s", logs.String())
	}
}
