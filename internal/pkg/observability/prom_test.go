package observability

import (
	"fmt"
	"sync"
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
		{ReportServerLabel, "CN", "CN"},
		{ReportServerLabel, "US", "US"},
		{ReportServerLabel, "JP", "JP"},
		{ReportServerLabel, "KR", "KR"},
		{ReportServerLabel, "cn", "other"},
		{ReportServerLabel, "", "other"},
	} {
		if got := tc.label(tc.value); got != tc.want {
			t.Errorf("label(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// TestBoundedLabel checks that a bounded label keeps its first values, maps the others to other, and keeps no more
// than its bound under concurrent use.
func TestBoundedLabel(t *testing.T) {
	b := boundedLabel{max: 3}
	for _, tc := range []struct{ value, want string }{
		{"MaaAssistantArknights", "MaaAssistantArknights"},
		{"frontend-v2", "frontend-v2"},
		{"MaaAssistantArknights", "MaaAssistantArknights"},
		{"gaop.core", "gaop.core"},
		{"random-1", "other"},
		{"frontend-v2", "frontend-v2"},
		{"random-2", "other"},
	} {
		if got := b.label(tc.value); got != tc.want {
			t.Errorf("label(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}

	b = boundedLabel{max: 10}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				b.label(fmt.Sprintf("source-%d-%d", g, i))
			}
		}(g)
	}
	wg.Wait()
	if n := len(b.values); n != 10 {
		t.Errorf("%d values kept, want 10", n)
	}

	// a value already kept costs no allocation
	b.label("source-0-0")
	kept := ""
	for v := range b.values {
		kept = v
		break
	}
	if n := testing.AllocsPerRun(100, func() { _ = b.label(kept) }); n != 0 {
		t.Errorf("label of a kept value allocates %v times, want 0", n)
	}
}
