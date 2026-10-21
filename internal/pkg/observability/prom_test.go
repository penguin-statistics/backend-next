package observability

import (
	"testing"

	"exusiai.dev/gommon/constant"
)

func TestReportLabels(t *testing.T) {
	for _, tc := range []struct {
		label func(string) string
		value string
		want  string
	}{
		{ReportSourceLabel, "MaaAssistantArknights", "maa"},
		{ReportSourceLabel, constant.MeoAssistant, "maa"},
		{ReportSourceLabel, constant.FrontendV2, "frontend"},
		{ReportSourceLabel, constant.FrontendV1, "frontend"},
		{ReportSourceLabel, constant.FrontendV1Internal, "frontend"},
		{ReportSourceLabel, "gaop.core", "other"},
		{ReportSourceLabel, "", "other"},
		{ReportSubjectLabel, "REPORT.SINGLE", "REPORT.SINGLE"},
		{ReportSubjectLabel, "REPORT.BATCH", "REPORT.BATCH"},
		{ReportSubjectLabel, "REPORT.UNKNOWN", "other"},
		{ReportSubjectLabel, "", "other"},
	} {
		if got := tc.label(tc.value); got != tc.want {
			t.Errorf("label(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
