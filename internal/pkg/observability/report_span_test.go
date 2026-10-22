package observability

import (
	"testing"

	"exusiai.dev/backend-next/internal/model/types"
)

func attributesOf(task *types.ReportTask) map[string]string {
	got := map[string]string{}
	for _, kv := range ReportTaskAttributes(task) {
		got[string(kv.Key)] = kv.Value.Emit()
	}
	return got
}

// A batch has the stage of its reports if they are all of one stage, and none otherwise; its drops and times are
// those of all its reports.
func TestReportTaskAttributesOfABatch(t *testing.T) {
	report := func(stage string, drops int) *types.ReportTaskSingleReport {
		return &types.ReportTaskSingleReport{
			FragmentStageID: types.FragmentStageID{StageID: stage},
			Drops:           make([]*types.Drop, drops),
			Times:           1,
		}
	}
	task := &types.ReportTask{
		TaskID:               "t",
		FragmentReportCommon: types.FragmentReportCommon{Server: "US", Source: "frontend-v2", Version: "v3.13.38"},
		Reports:              []*types.ReportTaskSingleReport{report("main_01-07", 2), report("main_01-07", 3)},
		AccountID:            7,
	}
	got := attributesOf(task)
	for k, want := range map[string]string{
		"report.task_id": "t", "report.server": "US", "report.source_category": "frontend", "report.count": "2",
		"report.stage_id": "main_01-07", "report.drops": "5", "report.times": "2", "usr.account_id": "7",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}

	task.Reports = append(task.Reports, report("main_04-04", 0))
	task.TaskID = ""
	got = attributesOf(task)
	if _, ok := got["report.stage_id"]; ok {
		t.Errorf("report.stage_id = %q for reports of several stages, want none", got["report.stage_id"])
	}
	if _, ok := got["report.task_id"]; ok {
		t.Error("report.task_id set for a task without an id")
	}
}
