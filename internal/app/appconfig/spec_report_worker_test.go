package appconfig

import (
	"testing"

	"github.com/kelseyhightower/envconfig"
)

// ReportWorkerEnabled keeps the report worker running unless the manifests turn it off, as before the flag existed,
// and is independent of WorkerEnabled (calcwkr).
func TestReportWorkerEnabled(t *testing.T) {
	for _, key := range []string{"DROP_REPORT_ARCHIVE_S3_BUCKET", "DROP_REPORT_ARCHIVE_S3_REGION", "AWS_ACCESS_KEY", "AWS_SECRET_KEY", "POSTGRES_DSN"} {
		t.Setenv("PENGUIN_V3_"+key, "x")
	}

	var defaults ConfigSpec
	if err := envconfig.Process("penguin_v3", &defaults); err != nil {
		t.Fatal(err)
	}
	if !defaults.ReportWorkerEnabled {
		t.Error("ReportWorkerEnabled is off by default")
	}

	t.Setenv("PENGUIN_V3_WORKER_ENABLED", "true")
	t.Setenv("PENGUIN_V3_REPORT_WORKER_ENABLED", "false")
	var set ConfigSpec
	if err := envconfig.Process("penguin_v3", &set); err != nil {
		t.Fatal(err)
	}
	if set.ReportWorkerEnabled {
		t.Error("PENGUIN_V3_REPORT_WORKER_ENABLED=false does not turn ReportWorkerEnabled off")
	}
	if !set.WorkerEnabled {
		t.Error("PENGUIN_V3_REPORT_WORKER_ENABLED=false turns WorkerEnabled off")
	}
}
