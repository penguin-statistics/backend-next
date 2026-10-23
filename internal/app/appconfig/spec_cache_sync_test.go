package appconfig

import (
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// CacheSyncInterval is on by default, so that the processes serving requests follow the refreshes of a worker
// running in another process, and 0 turns it off.
func TestCacheSyncInterval(t *testing.T) {
	t.Setenv("PENGUIN_V3_POSTGRES_DSN", "x")

	var defaults ConfigSpec
	if err := envconfig.Process("penguin_v3", &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.CacheSyncInterval != 10*time.Second {
		t.Errorf("CacheSyncInterval is %v by default, want 10s", defaults.CacheSyncInterval)
	}

	t.Setenv("PENGUIN_V3_CACHE_SYNC_INTERVAL", "0")
	var set ConfigSpec
	if err := envconfig.Process("penguin_v3", &set); err != nil {
		t.Fatal(err)
	}
	if set.CacheSyncInterval != 0 {
		t.Errorf("PENGUIN_V3_CACHE_SYNC_INTERVAL=0 sets CacheSyncInterval to %v, want 0", set.CacheSyncInterval)
	}
}
