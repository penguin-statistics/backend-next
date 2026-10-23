package service

// Integration test of the report recall on a real PostgreSQL and Redis, skipped unless PENGUIN_TEST_POSTGRES_DSN and
// PENGUIN_TEST_REDIS_URL are set, see internal/repo/integration_test.go and internal/workers/calcwkr/lock_test.go.

import (
	"context"
	"errors"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/redis/go-redis/v9"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo"
)

func TestRecallSingularReport(t *testing.T) {
	url := os.Getenv("PENGUIN_TEST_REDIS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_REDIS_URL is not set")
	}
	db := newTestDB(t)
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { rdb.Close() })
	s := &Report{Redis: rdb, DropReportRepo: repo.NewDropReport(db)}
	ctx := context.Background()

	// the recall key of a task names its report, as the report worker sets it
	newKey := func(reportId int) (hash string) {
		hash = "test-" + strconv.FormatUint(rand.Uint64(), 36)
		if err := rdb.Set(ctx, constant.ReportRedisPrefix+hash, reportId, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rdb.Del(context.Background(), constant.ReportRedisPrefix+hash) })
		return hash
	}
	recall := func(hash string) error {
		return s.RecallSingularReport(ctx, &types.SingularReportRecallRequest{ReportHash: hash})
	}
	keyExists := func(hash string) bool {
		n, err := rdb.Exists(ctx, constant.ReportRedisPrefix+hash).Result()
		if err != nil {
			t.Fatal(err)
		}
		return n == 1
	}

	mustExec(t, db, "INSERT INTO drop_reports (stage_id, pattern_id, times, reliability, server, account_id) VALUES (1, 1, 1, 0, 'CN', 1)")
	hash := newKey(1)

	if err := recall(hash); err != nil {
		t.Fatal(err)
	}
	var reliability int
	if err := db.NewRaw("SELECT reliability FROM drop_reports WHERE report_id = 1").Scan(ctx, &reliability); err != nil {
		t.Fatal(err)
	}
	if reliability != -1 {
		t.Errorf("reliability of the recalled report = %d, want -1", reliability)
	}
	if keyExists(hash) {
		t.Error("the recall key is left after the recall")
	}
	if err := recall(hash); !errors.Is(err, ErrReportNotFound) {
		t.Errorf("second recall: got %v, want ErrReportNotFound", err)
	}

	// a key naming a report that is not stored
	missing := newKey(2)
	if err := recall(missing); !errors.Is(err, ErrReportNotFound) {
		t.Errorf("recall of a missing report: got %v, want ErrReportNotFound", err)
	}
	if !keyExists(missing) {
		t.Error("the recall key of a missing report was deleted")
	}

	// a report submitted more than RecallMaxAge ago is not looked up (see repo.RecallMaxAge)
	mustExec(t, db, "INSERT INTO drop_reports (stage_id, pattern_id, times, reliability, server, account_id, created_at) VALUES (1, 1, 1, 0, 'CN', 1, now() - ? * interval '1 hour')",
		int(repo.RecallMaxAge/time.Hour)+1)
	old := newKey(2)
	if err := recall(old); !errors.Is(err, ErrReportNotFound) {
		t.Errorf("recall of a report older than RecallMaxAge: got %v, want ErrReportNotFound", err)
	}
	if err := db.NewRaw("SELECT reliability FROM drop_reports WHERE report_id = 2").Scan(ctx, &reliability); err != nil {
		t.Fatal(err)
	}
	if reliability != 0 {
		t.Errorf("reliability of the report older than RecallMaxAge = %d, want 0", reliability)
	}
}
