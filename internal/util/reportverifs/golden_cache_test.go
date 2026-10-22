package reportverifs

import (
	"context"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo"
)

// fakeClock is a clock set by hand
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// acceptedTask returns the first task of the fixture whose first report production accepted, and has drops.
func (f *goldenFixture) acceptedTask(t *testing.T) *types.ReportTask {
	t.Helper()
	for i := range f.Tasks {
		if r := f.Tasks[i].Reports[0]; r.Reliability == 0 && len(r.Drops) > 0 {
			return f.task(i)
		}
	}
	t.Fatal("no task of reliability 0 with drops")
	return nil
}

// verifierTest is a Verifier on the fixture with a clock set by hand, and the first accepted report of the fixture
type verifierTest struct {
	t     *testing.T
	db    *bun.DB
	clock *fakeClock
	v     *Verifier
	task  *types.ReportTask
}

func newVerifierTest(t *testing.T) *verifierTest {
	db, f := loadGolden(t)
	clock := &fakeClock{t: time.Now()}
	return &verifierTest{
		t:     t,
		db:    db,
		clock: clock,
		v:     &Verifier{DropInfoRepo: repo.NewDropInfo(db), RejectRuleRepo: repo.NewRejectRule(db), now: clock.now},
		task:  f.acceptedTask(t),
	}
}

func (vt *verifierTest) reliability() int {
	vt.t.Helper()
	r, _, err := vt.v.Reliability(context.Background(), vt.task, vt.task.Reports[0])
	if err != nil {
		vt.t.Fatal(err)
	}
	return r
}

func (vt *verifierTest) exec(query string, args ...any) {
	vt.t.Helper()
	if _, err := vt.db.ExecContext(context.Background(), query, args...); err != nil {
		vt.t.Fatal(err)
	}
}

// TestRejectRuleEditUsedAfterTTL checks that an edit of the reject rules is used once the rules read before it are
// cacheTTL old, and not before.
func TestRejectRuleEditUsedAfterTTL(t *testing.T) {
	vt := newVerifierTest(t)

	read := vt.clock.t
	if r := vt.reliability(); r != 0 {
		t.Fatalf("reliability %d before the edit, want 0", r)
	}
	// a rule matching every report, first by rule id
	vt.exec(`INSERT INTO reject_rules (rule_id, status, expr, with_reliability) VALUES (0, 1, 'true', 300)`)
	vt.clock.t = read.Add(cacheTTL - time.Nanosecond)
	if r := vt.reliability(); r != 0 {
		t.Fatalf("reliability %d before the rules read are cacheTTL old, want 0 as read before the edit", r)
	}
	vt.clock.t = read.Add(cacheTTL)
	if r := vt.reliability(); r != 300 {
		t.Fatalf("reliability %d once the rules read are cacheTTL old, want 300 of the new rule", r)
	}

	read = vt.clock.t
	vt.exec(`UPDATE reject_rules SET status = 0 WHERE rule_id = 0`)
	vt.clock.t = read.Add(cacheTTL / 2)
	if r := vt.reliability(); r != 300 {
		t.Fatalf("reliability %d before the rules read are cacheTTL old, want 300 of the rule as read", r)
	}
	vt.clock.t = read.Add(cacheTTL)
	if r := vt.reliability(); r != 0 {
		t.Fatalf("reliability %d once the rules read are cacheTTL old, want 0 without the deactivated rule", r)
	}

	// an error is not cached: the rules are read again on the next report
	vt.exec(`ALTER TABLE reject_rules RENAME TO reject_rules_gone`)
	vt.clock.t = vt.clock.t.Add(cacheTTL)
	if r, _, err := vt.v.Reliability(context.Background(), vt.task, vt.task.Reports[0]); err == nil {
		t.Fatalf("without reject_rules: reliability %d, want an error", r)
	}
	vt.exec(`ALTER TABLE reject_rules_gone RENAME TO reject_rules`)
	if r := vt.reliability(); r != 0 {
		t.Fatalf("reliability %d after the error, want 0", r)
	}
}

// stageDropInfosWhere selects the drop infos of the stage and server of the report of vt.
func (vt *verifierTest) stageDropInfosWhere() (string, []any) {
	return `server = ? AND stage_id = (SELECT stage_id FROM stages WHERE ark_stage_id = ?)`,
		[]any{vt.task.Server, vt.task.Reports[0].StageID}
}

// TestDropInfoEditUsedAfterTTL checks that an edit of the drop infos is used once the drop infos read before it are
// cacheTTL old, and not before.
func TestDropInfoEditUsedAfterTTL(t *testing.T) {
	vt := newVerifierTest(t)
	where, args := vt.stageDropInfosWhere()

	read := vt.clock.t
	if r := vt.reliability(); r != 0 {
		t.Fatalf("reliability %d before the edit, want 0", r)
	}
	// none of the drops of the report fits any more
	vt.exec(`UPDATE drop_infos SET item_id = item_id + 100000 WHERE item_id IS NOT NULL AND `+where, args...)
	vt.clock.t = read.Add(cacheTTL - time.Nanosecond)
	if r := vt.reliability(); r != 0 {
		t.Fatalf("reliability %d before the drop infos read are cacheTTL old, want 0 as read before the edit", r)
	}
	vt.clock.t = read.Add(cacheTTL)
	if r := vt.reliability(); r != constant.ViolationReliabilityDrop {
		t.Fatalf("reliability %d once the drop infos read are cacheTTL old, want %d", r, constant.ViolationReliabilityDrop)
	}
}

// TestDropInfosExpireWithTheirTimeRange checks that drop infos are read again when one of their time ranges ends, before
// they are cacheTTL old.
func TestDropInfosExpireWithTheirTimeRange(t *testing.T) {
	vt := newVerifierTest(t)
	where, args := vt.stageDropInfosWhere()
	ctx := context.Background()

	query := &repo.DropInfoQuery{Server: vt.task.Server, ArkStageId: vt.task.Reports[0].StageID}
	until := func() time.Time {
		t.Helper()
		_, _, until, err := vt.v.DropInfoRepo.GetForCurrentTimeRangeWithDropTypesUntil(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		return until
	}
	// the time ranges of the fixture never end
	if u := until(); !u.IsZero() {
		t.Fatalf("until %v, want zero", u)
	}
	// the time ranges of the stage end soon (by the clock of the database too), within cacheTTL
	end := vt.clock.t.Add(10 * time.Second).Truncate(time.Microsecond)
	vt.exec(`UPDATE time_ranges SET end_time = ? WHERE range_id IN (SELECT range_id FROM drop_infos WHERE `+where+`)`,
		append([]any{end}, args...)...)
	if u := until(); !u.Equal(end) {
		t.Fatalf("until %v, want the end of the time ranges %v", u, end)
	}

	if r := vt.reliability(); r != 0 {
		t.Fatalf("reliability %d before the edit, want 0", r)
	}
	vt.exec(`UPDATE drop_infos SET item_id = item_id + 100000 WHERE item_id IS NOT NULL AND `+where, args...)
	vt.clock.t = end.Add(-time.Nanosecond)
	if r := vt.reliability(); r != 0 {
		t.Fatalf("reliability %d before the time ranges end, want 0 as read before the edit", r)
	}
	vt.clock.t = end
	if r := vt.reliability(); r != constant.ViolationReliabilityDrop {
		t.Fatalf("reliability %d once the time ranges end, want %d as read again", r, constant.ViolationReliabilityDrop)
	}
}
