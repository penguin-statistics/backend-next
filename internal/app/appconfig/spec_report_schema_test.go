package appconfig

import (
	"testing"

	"github.com/kelseyhightower/envconfig"
)

// ReportSchemaV2Enabled is set by an environment variable in the manifests, and must stay off unless set: today's
// database has no drop_report_extras.created_at.
func TestReportSchemaV2Enabled(t *testing.T) {
	for _, key := range []string{"DROP_REPORT_ARCHIVE_S3_BUCKET", "DROP_REPORT_ARCHIVE_S3_REGION", "AWS_ACCESS_KEY", "AWS_SECRET_KEY", "POSTGRES_DSN"} {
		t.Setenv("PENGUIN_V3_"+key, "x")
	}

	var defaults ConfigSpec
	if err := envconfig.Process("penguin_v3", &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.ReportSchemaV2Enabled {
		t.Error("ReportSchemaV2Enabled is on by default")
	}

	t.Setenv("PENGUIN_V3_REPORT_SCHEMA_V2_ENABLED", "true")
	var set ConfigSpec
	if err := envconfig.Process("penguin_v3", &set); err != nil {
		t.Fatal(err)
	}
	if !set.ReportSchemaV2Enabled {
		t.Error("PENGUIN_V3_REPORT_SCHEMA_V2_ENABLED=true does not turn ReportSchemaV2Enabled on")
	}
}
