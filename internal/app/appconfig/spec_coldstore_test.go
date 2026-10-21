package appconfig

import (
	"testing"

	"github.com/kelseyhightower/envconfig"

	"exusiai.dev/backend-next/internal/pkg/duckdbx"
)

func TestColdstoreSettings(t *testing.T) {
	// the settings required without a default
	for _, key := range []string{"DROP_REPORT_ARCHIVE_S3_BUCKET", "DROP_REPORT_ARCHIVE_S3_REGION", "AWS_ACCESS_KEY", "AWS_SECRET_KEY", "POSTGRES_DSN"} {
		t.Setenv("PENGUIN_V3_"+key, "x")
	}

	var defaults ConfigSpec
	if err := envconfig.Process("penguin_v3", &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.ColdstoreExtensionDirectory != duckdbx.ImageExtensionDirectory {
		t.Errorf("ColdstoreExtensionDirectory = %q, want the image's %q", defaults.ColdstoreExtensionDirectory, duckdbx.ImageExtensionDirectory)
	}

	t.Setenv("PENGUIN_V3_COLDSTORE_EXTENSION_DIRECTORY", "/tmp/ext")
	var set ConfigSpec
	if err := envconfig.Process("penguin_v3", &set); err != nil {
		t.Fatal(err)
	}
	if set.ColdstoreExtensionDirectory != "/tmp/ext" {
		t.Errorf("ColdstoreExtensionDirectory = %q, want /tmp/ext", set.ColdstoreExtensionDirectory)
	}
}
