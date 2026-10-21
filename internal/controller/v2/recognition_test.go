package v2

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/go-playground/validator/v10"

	"exusiai.dev/backend-next/internal/util/rekuest"
)

func TestInitDefectReportRequestValidation(t *testing.T) {
	const environment = `{
		"frontendVersion": "v3.12.0",
		"frontendCommit": "abcdef0",
		"recognizerVersion": "v4.2.1",
		"recognizerOpenCVVersion": "4.5.5",
		"recognizerAssetsVersion": "1.0.0",
		"server": "CN",
		"sessionId": "a1B2c3D4"
	}`

	tests := []struct {
		name    string
		payload string
		want    []string
	}{
		{"valid", `{"recognitionResult": {"stageId": "main_01-07"}, "environment": ` + environment + `}`, []string{}},
		{"valid without recognition result", `{"environment": ` + environment + `}`, []string{}},
		{
			"invalid environment",
			`{"environment": {"frontendVersion": "v3.12.0", "frontendCommit": "abcdef0", "recognizerVersion": "v4.2.1",
				"recognizerOpenCVVersion": "4.5.5", "recognizerAssetsVersion": "1.0.0", "server": "XX", "sessionId": "short"}}`,
			[]string{"InitDefectReportRequest.Environment.Server:arkserver", "InitDefectReportRequest.Environment.SessionID:len"},
		},
		{
			"missing environment", `{"recognitionResult": {}}`,
			[]string{
				"InitDefectReportRequest.Environment.FrontendCommit:required",
				"InitDefectReportRequest.Environment.FrontendVersion:required",
				"InitDefectReportRequest.Environment.RecognizerAssetsVersion:required",
				"InitDefectReportRequest.Environment.RecognizerOpenCVVersion:required",
				"InitDefectReportRequest.Environment.RecognizerVersion:required",
				"InitDefectReportRequest.Environment.Server:required",
				"InitDefectReportRequest.Environment.SessionID:required",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req InitDefectReportRequest
			if err := json.Unmarshal([]byte(tt.payload), &req); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("validator panicked: %v", r)
				}
			}()

			got := []string{}
			if err := rekuest.Validate.Struct(req); err != nil {
				errs, ok := err.(validator.ValidationErrors)
				if !ok {
					t.Fatalf("unexpected validation error type %T: %v", err, err)
				}
				for _, fe := range errs {
					got = append(got, fmt.Sprintf("%s:%s", fe.Namespace(), fe.Tag()))
				}
				sort.Strings(got)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("violations = %v, want %v", got, tt.want)
			}
		})
	}
}
