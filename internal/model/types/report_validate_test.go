package types_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/go-playground/validator/v10"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/util/rekuest"
)

// validate unmarshals payload into a new T and runs it through the validator singleton the
// handlers use. It returns the violations as sorted "namespace:tag" strings, and fails the
// test instead of crashing it when the validator panics (e.g. on a bad dive tag).
func validate[T any](t *testing.T, payload string) (violations []string) {
	t.Helper()

	var dest T
	if err := json.Unmarshal([]byte(payload), &dest); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("validator panicked: %v", r)
		}
	}()

	err := rekuest.Validate.Struct(dest)
	if err == nil {
		return []string{}
	}
	errs, ok := err.(validator.ValidationErrors)
	if !ok {
		t.Fatalf("unexpected validation error type %T: %v", err, err)
	}
	for _, fe := range errs {
		violations = append(violations, fmt.Sprintf("%s:%s", fe.Namespace(), fe.Tag()))
	}
	sort.Strings(violations)
	return violations
}

const (
	validMetadata = `{
		"fingerprint": "f1f2f3",
		"md5": "d41d8cd98f00b204e9800998ecf8427e",
		"fileName": "Screenshot_20231001.png",
		"lastModified": 1696118400000,
		"recognizerVersion": "v4.2.1",
		"recognizerAssetsVersion": "1.0.0+abcdef"
	}`
	invalidMetadata = `{
		"md5": "d41d8cd98f00b204e9800998ecf8427e0",
		"recognizerVersion": "latest"
	}`
)

func singularReport(extra string) string {
	return `{
		"stageId": "main_01-07",
		"server": "CN",
		"source": "frontend-v2",
		"version": "v3.0.0",
		"drops": [{"dropType": "NORMAL_DROP", "itemId": "30012", "quantity": 2}],
		"times": 1` + extra + `
	}`
}

func batchReport(metadata string) string {
	return `{
		"server": "CN",
		"source": "frontend-v2-recognition",
		"version": "v3.0.0",
		"batchDrops": [{
			"stageId": "main_01-07",
			"drops": [{"dropType": "NORMAL_DROP", "itemId": "30012", "quantity": 2}],
			"metadata": ` + metadata + `
		}]
	}`
}

func TestReportRequestValidation(t *testing.T) {
	tests := []struct {
		name     string
		validate func(t *testing.T, payload string) []string
		payload  string
		want     []string
	}{
		{"singular/no metadata", validate[types.SingularReportRequest], singularReport(``), []string{}},
		{"singular/null metadata", validate[types.SingularReportRequest], singularReport(`, "metadata": null`), []string{}},
		{"singular/empty metadata", validate[types.SingularReportRequest], singularReport(`, "metadata": {}`), []string{}},
		{"singular/valid metadata", validate[types.SingularReportRequest], singularReport(`, "metadata": ` + validMetadata), []string{}},
		{
			"singular/invalid metadata", validate[types.SingularReportRequest], singularReport(`, "metadata": ` + invalidMetadata),
			[]string{"SingularReportRequest.Metadata.MD5:lte", "SingularReportRequest.Metadata.RecognizerVersion:semverprefixed"},
		},
		{
			"singular/invalid drop and times", validate[types.SingularReportRequest],
			`{"stageId": "main_01-07", "server": "CN", "source": "s", "version": "v",
				"drops": [{"dropType": "UNKNOWN_DROP", "itemId": "30012", "quantity": 1001}], "times": 11}`,
			[]string{"SingularReportRequest.Drops[0].DropType:oneof", "SingularReportRequest.Drops[0].Quantity:lte", "SingularReportRequest.Times:lte"},
		},
		{
			"singular/missing common fields", validate[types.SingularReportRequest], `{"drops": []}`,
			[]string{
				"SingularReportRequest.FragmentReportCommon.Server:required",
				"SingularReportRequest.FragmentReportCommon.Source:required",
				"SingularReportRequest.FragmentReportCommon.Version:required",
				"SingularReportRequest.FragmentStageID.StageID:required",
			},
		},

		// the batch (recognition) handler does not validate its request today; these only guard against panics
		{"batch/empty metadata", validate[types.BatchReportRequest], batchReport(`{}`), []string{}},
		{"batch/valid metadata", validate[types.BatchReportRequest], batchReport(validMetadata), []string{}},
		{
			"batch/invalid metadata", validate[types.BatchReportRequest], batchReport(invalidMetadata),
			[]string{"BatchReportRequest.BatchDrops[0].Metadata.MD5:lte", "BatchReportRequest.BatchDrops[0].Metadata.RecognizerVersion:semverprefixed"},
		},

		// the queued task is not validated either; same as above
		{"task/no metadata", validate[types.ReportTaskSingleReport], `{"stageId": "main_01-07", "drops": [], "times": 1}`, []string{}},
		{"task/valid metadata", validate[types.ReportTaskSingleReport], `{"stageId": "main_01-07", "drops": [], "times": 1, "metadata": ` + validMetadata + `}`, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.validate(t, tt.payload)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("violations = %v, want %v", got, tt.want)
			}
		})
	}
}
