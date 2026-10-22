package observability

import (
	"strconv"

	"go.opentelemetry.io/otel/attribute"

	"exusiai.dev/backend-next/internal/model/types"
)

// The attributes of the spans of a report task: the server span of the request that submitted it, the span of its
// publishing and the span of its processing (reportwkr). They name what the task is about, so that the traces of
// reports can be searched and grouped by it in Datadog (e.g. @report.source:MeoAssistant @report.server:CN).
const (
	AttributeReportTaskID = attribute.Key("report.task_id")
	// the server of the game the reports are of: CN, US, JP or KR
	AttributeReportServer = attribute.Key("report.server")
	// the source of the reports as the client sent it (free-form, e.g. MeoAssistant, frontend-v2), and its version
	AttributeReportSource        = attribute.Key("report.source")
	AttributeReportSourceVersion = attribute.Key("report.source_version")
	// the source in the categories of the report metrics (ReportSourceLabel): maa, frontend or other
	AttributeReportSourceCategory = attribute.Key("report.source_category")
	// the number of reports in the task: 1 for a report of /report, any number for a batch (/report/recognition)
	AttributeReportCount = attribute.Key("report.count")
	// the stage of the reports (the game's stage id, as the client sent it), if they are all of the same stage
	AttributeReportStageID = attribute.Key("report.stage_id")
	// the drops of the reports (items after merging the same item and drop type) and their times (runs)
	AttributeReportDrops = attribute.Key("report.drops")
	AttributeReportTimes = attribute.Key("report.times")
	// the internal id of the account the reports are of (drop_reports.account_id), a string as in the request log
	AttributeUserAccountID = attribute.Key("usr.account_id")
)

// ReportTaskAttributes returns the attributes that describe task on its spans. It allocates: call it for a span that
// is recording only.
func ReportTaskAttributes(task *types.ReportTask) []attribute.KeyValue {
	drops, times := 0, 0
	stageID := ""
	for i, r := range task.Reports {
		drops += len(r.Drops)
		times += r.Times
		switch {
		case i == 0:
			stageID = r.StageID
		case r.StageID != stageID:
			stageID = ""
		}
	}
	attrs := make([]attribute.KeyValue, 0, 10)
	attrs = append(attrs,
		AttributeReportServer.String(task.Server),
		AttributeReportSource.String(task.Source),
		AttributeReportSourceVersion.String(task.Version),
		AttributeReportSourceCategory.String(ReportSourceLabel(task.Source)),
		AttributeReportCount.Int(len(task.Reports)),
		AttributeReportDrops.Int(drops),
		AttributeReportTimes.Int(times),
		AttributeUserAccountID.String(strconv.Itoa(task.AccountID)),
	)
	if task.TaskID != "" {
		attrs = append(attrs, AttributeReportTaskID.String(task.TaskID))
	}
	if stageID != "" {
		attrs = append(attrs, AttributeReportStageID.String(stageID))
	}
	return attrs
}
