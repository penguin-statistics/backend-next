package reportverifs

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/antonmedv/expr"
	"github.com/goccy/go-json"

	"exusiai.dev/backend-next/internal/model/types"
)

// testRejectRules are the active reject rules of production, plus a rule that fails to compile and one that fails at
// run time on a report without metadata.
var testRejectRules = []string{
	`(
    (
        Task.Source == "frontend-v2-recognition" &&
        SemVerCompare(Report.Metadata.RecognizerVersion, "v4.2.2") < 0
    ) || (
        Task.Source == "MeoAssistant" &&
        SemVerCompare(Task.Version, "v3.3.2") < 0
    )
) && (
    (
        Report.StageID in ["main_00-01", "main_00-02", "main_00-03", "main_00-04", "main_00-05", "main_00-06", "main_00-07", "main_00-08", "main_00-09", "main_00-10", "main_00-11", "main_01-01", "main_01-03", "main_01-04", "main_01-05", "main_01-06", "main_01-07", "main_01-08", "main_01-09", "main_01-10", "main_01-12", "main_02-01", "main_02-02", "main_02-03", "main_02-04", "main_02-05", "main_02-06", "main_02-07", "main_02-08", "main_02-09", "sub_02-01", "sub_02-02", "sub_02-03", "sub_02-04", "sub_02-05", "sub_02-06", "sub_02-07", "sub_02-08", "sub_02-09", "sub_02-10", "sub_02-11", "main_03-06", "wk_armor_3", "wk_armor_4", "wk_armor_5", "main_07-09", "sub_03-3-2", "main_08-05", "main_08-09", "main_09-14", "tough_10-03", "main_10-03", "tough_10-14", "main_10-14"] &&
        Task.CreatedAt > 1651089600000000 &&
        Task.Server == "CN"
    ) || (
        Report.StageID in ["act17side_01", "act17side_02", "act17side_03", "act17side_04", "act17side_05", "act17side_07"] &&
        Task.Server == "CN"
    )
)`,
	`(
    Task.Source == "MeoAssistant" &&
    SemVerCompare(Task.Version, "v4.3.0-beta.3") < 0 &&
    Report.StageID == "recruit"
)`,
	`(
    Task.Source == "MeoAssistant" &&
    Task.Version matches "v?4\\.7\\.0-.*" &&
    Task.CreatedAt > 1668618000000000
)`,
	`(
    Task.Source == "MeoAssistant" &&
    SemVerCompare(Task.Version, "v4.13.0-rc.1") < 0 &&
    Task.CreatedAt > 1680768000000000 &&
    Task.CreatedAt < 1681934400000000
)`,
	`(
    Task.Server == "CN" &&
    Task.Source == "MeoAssistant" &&
    SemVerCompare(Task.Version, "v4.26.0") <= 0 &&
    Task.CreatedAt > 1698825600000000 &&
    Report.StageID != "recruit"
)`,
	`(
    Task.Server == "CN" &&
    Task.CreatedAt > 1732550400000000 &&
    Report.StageID in ["act29side_01_rep", "act29side_02_rep", "act29side_03_rep", "act29side_04_rep", "act29side_05_rep", "act29side_06_rep", "act29side_07_rep", "act29side_08_rep", "act29side_09_rep", "act29side_10_rep"]
)`,
	`(
    Task.AccountID == 7854762 || Task.IP == "182.90.207.3/32" || Task.IP == "182.90.207.57/32"
)`,
	`Task.Source == "MeoAssistant" &&`,
	`Report.Metadata.RecognizerVersion == "v4.2.2"`,
}

// testRejectRuleContexts returns the contexts of the reports of the consumer benchmark's tasks, if its fixtures are
// there, and of the combinations of the values the rules test.
func testRejectRuleContexts(t *testing.T) []ReportContext {
	var contexts []ReportContext
	if f, err := os.Open("../../workers/reportwkr/testdata/consumerbench/tasks.jsonl.gz"); err == nil {
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(zr)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var line struct {
				Data *types.ReportTask `json:"data"`
			}
			if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
				t.Fatal(err)
			}
			for _, report := range line.Data.Reports {
				contexts = append(contexts, ReportContext{Report: report, Task: line.Data})
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%d reports of the consumer benchmark's fixtures", len(contexts))

	for _, source := range []string{"MeoAssistant", "frontend-v2-recognition", "MaaAssistantArknights"} {
		for _, version := range []string{"", "v3.3.1", "v4.2.0", "v4.3.0-beta.2", "v4.7.0-beta.1", "4.7.0-rc", "v4.12.9", "v4.26.0", "v5.0.0"} {
			for _, server := range []string{"CN", "US"} {
				for _, stageID := range []string{"main_01-07", "act17side_03", "recruit", "act29side_05_rep", "main_04-04"} {
					for _, createdAt := range []int64{1600000000000000, 1669000000000000, 1681000000000000, 1700000000000000, 1792434445257374} {
						for _, metadata := range []*types.ReportRequestMetadata{nil, {RecognizerVersion: "v4.2.1"}, {RecognizerVersion: "v4.2.2"}} {
							task := &types.ReportTask{CreatedAt: createdAt, AccountID: 1414, IP: "203.0.113.1"}
							task.Source, task.Version, task.Server = source, version, server
							report := &types.ReportTaskSingleReport{Metadata: metadata}
							report.StageID = stageID
							task.Reports = []*types.ReportTaskSingleReport{report}
							contexts = append(contexts, ReportContext{Report: report, Task: task})
						}
					}
				}
			}
		}
	}
	for _, account := range []struct {
		id int
		ip string
	}{{7854762, "203.0.113.1"}, {1, "182.90.207.3/32"}, {1, "182.90.207.57/32"}} {
		task := &types.ReportTask{AccountID: account.id, IP: account.ip}
		report := &types.ReportTaskSingleReport{}
		report.StageID = "main_01-07"
		contexts = append(contexts, ReportContext{Report: report, Task: task})
	}
	return contexts
}

// TestCompiledRuleEvaluatesAsEval pins that running the cached program of a rule gives the result and the error that
// expr.Eval gives for every report, from several goroutines at once (run it with -race).
func TestCompiledRuleEvaluatesAsEval(t *testing.T) {
	contexts := testRejectRuleContexts(t)
	matched, failed := map[int]int{}, map[int]int{}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, rule := range testRejectRules {
				for _, reportContext := range contexts {
					want, wantErr := expr.Eval(rule, reportContext)

					var got any
					prog, err := compiledRule(rule)
					if err == nil {
						got, err = expr.Run(prog, reportContext)
					}

					if fmt.Sprint(err) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) {
						t.Errorf("rule %d on %+v %+v: got (%v, %v), want (%v, %v)", i, *reportContext.Task, *reportContext.Report, got, err, want, wantErr)
						return
					}
					mu.Lock()
					if want == true {
						matched[i]++
					}
					if wantErr != nil {
						failed[i]++
					}
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	// each production rule matches some of the reports (the first one also fails on the reports of the recognition
	// without metadata), the one that does not compile fails on all of them, the last one on those without metadata
	for i := range testRejectRules[:7] {
		if matched[i] == 0 {
			t.Errorf("rule %d matched none of the reports", i)
		}
	}
	if failed[7] != 4*len(contexts) || failed[8] == 0 || failed[8] == 4*len(contexts) {
		t.Errorf("the broken rules failed %d and %d of %d evaluations", failed[7], failed[8], 4*len(contexts))
	}
}

func TestCompiledRuleCacheIsBounded(t *testing.T) {
	for i := 0; i <= programCacheMaxEntries; i++ {
		if _, err := compiledRule(fmt.Sprintf("Task.AccountID == %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	programCacheMu.RLock()
	n := len(programCache)
	programCacheMu.RUnlock()
	if n > programCacheMaxEntries {
		t.Fatalf("the cache holds %d programs, more than %d", n, programCacheMaxEntries)
	}
}
