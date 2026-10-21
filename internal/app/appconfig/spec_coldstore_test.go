package appconfig

import (
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"

	"exusiai.dev/backend-next/internal/pkg/duckdbx"
)

func TestColdstoreSettings(t *testing.T) {
	// the settings required without a default
	t.Setenv("PENGUIN_V3_POSTGRES_DSN", "x")

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

// The monthly archiving drops partitions: it must stay off unless turned on, and the old archiver's settings are no
// longer required.
func TestColdstoreArchiveSettings(t *testing.T) {
	t.Setenv("PENGUIN_V3_POSTGRES_DSN", "x")
	var defaults ConfigSpec
	if err := envconfig.Process("penguin_v3", &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.ColdstoreArchiveEnabled {
		t.Error("ColdstoreArchiveEnabled is on by default")
	}
	if defaults.ColdstoreArchiveAfter != 60*24*time.Hour || defaults.ColdstoreS3Region != "ap-southeast-1" ||
		defaults.ColdstoreS3Prefix != "coldstore/v1" || defaults.ColdstoreS3StorageClass != "STANDARD_IA" ||
		defaults.ColdstoreArchiveMinFreeBytes != 10<<30 || defaults.ColdstoreArchiveDropDelay != 15*time.Minute {
		t.Errorf("defaults: %+v", defaults)
	}

	t.Setenv("PENGUIN_V3_COLDSTORE_ARCHIVE_ENABLED", "true")
	t.Setenv("PENGUIN_V3_COLDSTORE_S3_STORAGE_CLASS", "GLACIER_IR")
	var set ConfigSpec
	if err := envconfig.Process("penguin_v3", &set); err != nil {
		t.Fatal(err)
	}
	if !set.ColdstoreArchiveEnabled || set.ColdstoreS3StorageClass != "GLACIER_IR" {
		t.Errorf("set: enabled %v, storage class %q", set.ColdstoreArchiveEnabled, set.ColdstoreS3StorageClass)
	}
}
