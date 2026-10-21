package appconfig

import (
	"testing"
	"time"

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
	// the reader is off unless enabled, with the limits of #752
	if defaults.ColdstoreEnabled || defaults.ColdstoreDirectory != "" || defaults.ColdstoreTempDirectory != "" {
		t.Errorf("cold storage reader enabled by default: %v %q %q", defaults.ColdstoreEnabled, defaults.ColdstoreDirectory, defaults.ColdstoreTempDirectory)
	}
	if defaults.ColdstoreQueriesEnabled {
		t.Error("cold storage queries enabled by default")
	}
	if defaults.ColdstoreShadowEnabled || defaults.ColdstoreShadowTimeout != 30*time.Second || defaults.ColdstoreShadowMaxInFlight != 2 {
		t.Errorf("ColdstoreShadowEnabled = %v, ColdstoreShadowTimeout = %v, ColdstoreShadowMaxInFlight = %d, want false, 30s, 2",
			defaults.ColdstoreShadowEnabled, defaults.ColdstoreShadowTimeout, defaults.ColdstoreShadowMaxInFlight)
	}
	if defaults.ColdstoreThreads != 2 || defaults.ColdstoreMemoryLimit != "1GB" {
		t.Errorf("ColdstoreThreads = %d, ColdstoreMemoryLimit = %q, want 2, 1GB", defaults.ColdstoreThreads, defaults.ColdstoreMemoryLimit)
	}
	if defaults.ColdstoreReloadInterval != time.Minute || defaults.ColdstoreIntegrityCheckInterval != 7*24*time.Hour {
		t.Errorf("ColdstoreReloadInterval = %v, ColdstoreIntegrityCheckInterval = %v", defaults.ColdstoreReloadInterval, defaults.ColdstoreIntegrityCheckInterval)
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
