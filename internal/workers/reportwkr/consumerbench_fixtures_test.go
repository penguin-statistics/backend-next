//go:build consumerbench

package reportwkr

// The fixture generator of the consumer benchmark (consumerbench_test.go). It writes consumerBenchFixtures: report
// tasks encoded as the report handler publishes them, drawn from the shapes of recent production reports (stage,
// server, times, source, version, drop pattern) that scripts/consumerbench/setup-db.sh samples into the scratch
// database. Account ids, IPs, task ids and image metadata are synthetic. Run it with
//
//	CONSUMERBENCH_GENERATE=1 go test -tags consumerbench -run TestConsumerBenchGenerate ./internal/workers/reportwkr/

import (
	"compress/gzip"
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/goccy/go-json"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/util/reportverifs"
)

const consumerBenchFixtures = "testdata/consumerbench/tasks.jsonl.gz"

// consumerBenchFixture is a line of consumerBenchFixtures: a task as published under subject.
type consumerBenchFixture struct {
	Subject string          `json:"subject"`
	Data    json.RawMessage `json:"data"`
}

type reportShape struct {
	ArkStageID string    `bun:"ark_stage_id"`
	Server     string    `bun:"server"`
	Times      int       `bun:"times"`
	Source     string    `bun:"source_name"`
	Version    string    `bun:"version"`
	PatternID  int       `bun:"pattern_id"`
	IPFamily   int       `bun:"ip_family"`
	CreatedAt  time.Time `bun:"created_at"`
	// OriginalFingerprint is item:quantity|item:quantity... of the pattern
	OriginalFingerprint string `bun:"original_fingerprint"`
}

func envInt(t testing.TB, name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return n
}

// TestConsumerBenchGenerate writes CONSUMERBENCH_TASKS (default 1000) tasks, CONSUMERBENCH_BATCH_PERMILLE (default 20)
// of them batch tasks of 2 to 8 reports with image metadata as the recognition of the website sends, the others
// singular reports without metadata as MAA sends (in October 2026, 6 tasks in 200,000 were batches and no report had
// metadata).
func TestConsumerBenchGenerate(t *testing.T) {
	if os.Getenv("CONSUMERBENCH_GENERATE") == "" {
		t.Skip("CONSUMERBENCH_GENERATE is not set")
	}
	n := envInt(t, "CONSUMERBENCH_TASKS", 1000)
	batchPermille := envInt(t, "CONSUMERBENCH_BATCH_PERMILLE", 20)
	rng := rand.New(rand.NewSource(int64(envInt(t, "CONSUMERBENCH_SEED", 1))))
	ctx := context.Background()
	db := openBenchDB(t, nil)
	defer db.Close()

	var shapes []reportShape
	if err := db.NewRaw(`SELECT s.ark_stage_id, s.server, s.times, coalesce(s.source_name, '') AS source_name,
			coalesce(s.version, '') AS version, s.pattern_id, s.ip_family, s.created_at, dp.original_fingerprint
		FROM consumerbench_report_sample s JOIN drop_patterns dp USING (pattern_id)
		ORDER BY s.created_at, s.pattern_id, s.ark_stage_id`).Scan(ctx, &shapes); err != nil {
		t.Fatal(err)
	}
	if len(shapes) == 0 {
		t.Fatal("consumerbench_report_sample is empty: run scripts/consumerbench/setup-db.sh")
	}
	dropInfoRepo := repo.NewDropInfo(db)
	verifier := reportverifs.Verifier{DropInfoRepo: dropInfoRepo, RejectRuleRepo: repo.NewRejectRule(db)}

	out, err := os.Create(consumerBenchFixtures)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	zw := gzip.NewWriter(out)

	reliabilities := map[int]int{}
	reports := 0
	for i := 0; i < n; i++ {
		var task *types.ReportTask
		subject := "REPORT.SINGLE"
		if rng.Intn(1000) < batchPermille {
			subject = "REPORT.BATCH"
			task = batchTask(t, rng, dropInfoRepo, shapes)
		} else {
			shape := shapes[rng.Intn(len(shapes))]
			task = &types.ReportTask{
				CreatedAt:            shape.CreatedAt.UnixMicro(),
				FragmentReportCommon: types.FragmentReportCommon{Server: shape.Server, Source: shape.Source, Version: shape.Version},
				Reports:              []*types.ReportTaskSingleReport{singleReport(t, dropInfoRepo, shape, shape.Times, nil)},
				IP:                   syntheticIP(rng, shape.IPFamily),
			}
		}
		task.TaskID = syntheticTaskID(rng)
		task.AccountID = rng.Intn(envInt(t, "CONSUMERBENCH_ACCOUNTS", 5000)) + 1

		data, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		line, err := json.Marshal(consumerBenchFixture{Subject: subject, Data: data})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := zw.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}

		for _, report := range task.Reports {
			reliability, _, err := verifier.Reliability(ctx, task, report)
			if err != nil {
				t.Fatal(err)
			}
			reliabilities[reliability]++
			reports++
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d tasks, %d reports, reliabilities %v", n, reports, reliabilities)
}

func batchTask(t *testing.T, rng *rand.Rand, dropInfoRepo *repo.DropInfo, shapes []reportShape) *types.ReportTask {
	// the website recognizes screenshots of battles of CN, one run each
	var cn []reportShape
	for len(cn) < 2+rng.Intn(7) {
		if shape := shapes[rng.Intn(len(shapes))]; shape.Server == "CN" && shape.Times == 1 && shape.ArkStageID != "recruit" {
			cn = append(cn, shape)
		}
	}
	task := &types.ReportTask{
		CreatedAt:            cn[0].CreatedAt.UnixMicro(),
		FragmentReportCommon: types.FragmentReportCommon{Server: "CN", Source: constant.FrontendV2 + "-recognition", Version: "v3.15.2"},
		IP:                   syntheticIP(rng, cn[0].IPFamily),
	}
	for i, shape := range cn {
		metadata := &types.ReportRequestMetadata{
			Fingerprint:             fmt.Sprintf("%016x", rng.Uint64()),
			MD5:                     fmt.Sprintf("%016x%016x", rng.Uint64(), rng.Uint64()),
			FileName:                fmt.Sprintf("Screenshot_20261019-%06d_%d.png", rng.Intn(240000), i),
			LastModified:            int(shape.CreatedAt.UnixMilli()) - rng.Intn(86400000),
			RecognizerVersion:       "v4.3.1",
			RecognizerAssetsVersion: "v1.0.0-1a2b3c4",
		}
		task.Reports = append(task.Reports, singleReport(t, dropInfoRepo, shape, 1, metadata))
	}
	return task
}

// singleReport returns a report of the stage of shape with the drops of its pattern, each item under the drop types its
// drop infos allow, in their order, each filled up to its upper bound before the next (the remainder to the last).
func singleReport(t *testing.T, dropInfoRepo *repo.DropInfo, shape reportShape, times int, metadata *types.ReportRequestMetadata) *types.ReportTaskSingleReport {
	itemDropInfos, _, err := dropInfoRepo.GetForCurrentTimeRangeWithDropTypes(context.Background(), &repo.DropInfoQuery{
		Server: shape.Server, ArkStageId: shape.ArkStageID,
	})
	if err != nil {
		t.Fatal(err)
	}
	byItem := map[int][]*model.DropInfo{}
	for _, dropInfo := range itemDropInfos {
		id := int(dropInfo.ItemID.Int64)
		byItem[id] = append(byItem[id], dropInfo)
	}
	order := map[string]int{"REGULAR": 0, "SPECIAL": 1, "EXTRA": 2, "FURNITURE": 3}
	for _, infos := range byItem {
		sort.SliceStable(infos, func(i, j int) bool { return order[infos[i].DropType] < order[infos[j].DropType] })
	}

	report := &types.ReportTaskSingleReport{
		FragmentStageID: types.FragmentStageID{StageID: shape.ArkStageID},
		Drops:           []*types.Drop{},
		Times:           times,
		Metadata:        metadata,
	}
	if shape.OriginalFingerprint == "" {
		return report
	}
	for _, segment := range strings.Split(shape.OriginalFingerprint, "|") {
		var itemId, quantity int
		if _, err := fmt.Sscanf(segment, "%d:%d", &itemId, &quantity); err != nil {
			t.Fatalf("fingerprint %q: %v", shape.OriginalFingerprint, err)
		}
		infos := byItem[itemId]
		if len(infos) == 0 {
			// an item the stage no longer drops: rejected by the verification, as such a report would be
			report.Drops = append(report.Drops, &types.Drop{DropType: "REGULAR", ItemID: itemId, Quantity: quantity})
			continue
		}
		for i, info := range infos {
			q := quantity
			if i < len(infos)-1 && info.Bounds != nil && q > info.Bounds.Upper*times {
				q = info.Bounds.Upper * times
			}
			if q > 0 {
				report.Drops = append(report.Drops, &types.Drop{DropType: info.DropType, ItemID: itemId, Quantity: q})
			}
			quantity -= q
			if quantity == 0 {
				break
			}
		}
	}
	return report
}

// syntheticIP returns an address of the documentation ranges of the family.
func syntheticIP(rng *rand.Rand, family int) string {
	if family == 6 {
		return fmt.Sprintf("2001:db8:%x:%x::%x", rng.Intn(1<<16), rng.Intn(1<<16), rng.Intn(1<<16))
	}
	nets := []string{"192.0.2", "198.51.100", "203.0.113"}
	return fmt.Sprintf("%s.%d", nets[rng.Intn(len(nets))], 1+rng.Intn(254))
}

// syntheticTaskID returns a task id shaped like the handler's: the request id (an xid) and 16 random characters.
func syntheticTaskID(rng *rand.Rand) string {
	const xid = "0123456789abcdefghijklmnopqrstuv"
	const uniuri = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 0, 37)
	for i := 0; i < 20; i++ {
		b = append(b, xid[rng.Intn(len(xid))])
	}
	b = append(b, '-')
	for i := 0; i < 16; i++ {
		b = append(b, uniuri[rng.Intn(len(uniuri))])
	}
	return string(b)
}

// loadConsumerBenchFixtures reads consumerBenchFixtures.
func loadConsumerBenchFixtures(t testing.TB) []consumerBenchFixture {
	f, err := os.Open(consumerBenchFixtures)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(zr)
	var fixtures []consumerBenchFixture
	for dec.More() {
		var fixture consumerBenchFixture
		if err := dec.Decode(&fixture); err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, fixture)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures")
	}
	return fixtures
}
