package reportverifs

import (
	"context"
	"testing"
	"time"

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
