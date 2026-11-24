package appconfig

import (
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// The incremental global matrices are off by default, and only off, verify and on are accepted.
func TestGlobalMatrixIncremental(t *testing.T) {
	t.Setenv("PENGUIN_V3_POSTGRES_DSN", "x")

	var defaults ConfigSpec
	if err := envconfig.Process("penguin_v3", &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.GlobalMatrixIncremental != GlobalMatrixIncrementalOff || defaults.GlobalMatrixIncremental.Incremental() {
		t.Errorf("GlobalMatrixIncremental is %q by default, want off", defaults.GlobalMatrixIncremental)
	}
	if defaults.GlobalMatrixBaseMaxAge != 24*time.Hour {
		t.Errorf("GlobalMatrixBaseMaxAge is %v by default, want 24h", defaults.GlobalMatrixBaseMaxAge)
	}
	if defaults.GlobalMatrixVerifyEvery != 0 {
		t.Errorf("GlobalMatrixVerifyEvery is %d by default, want 0", defaults.GlobalMatrixVerifyEvery)
	}
	if defaults.GlobalMatrixMaxChangedStages != 100 {
		t.Errorf("GlobalMatrixMaxChangedStages is %d by default, want 100", defaults.GlobalMatrixMaxChangedStages)
	}
	if (GlobalMatrixIncrementalMode("")).Incremental() {
		t.Error("the zero mode (a configuration built in code) is incremental, want off")
	}

	for value, want := range map[string]GlobalMatrixIncrementalMode{
		"off": GlobalMatrixIncrementalOff, "verify": GlobalMatrixIncrementalVerify, "on": GlobalMatrixIncrementalOn, " ON ": GlobalMatrixIncrementalOn,
	} {
		t.Setenv("PENGUIN_V3_GLOBAL_MATRIX_INCREMENTAL", value)
		var set ConfigSpec
		if err := envconfig.Process("penguin_v3", &set); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if set.GlobalMatrixIncremental != want {
			t.Errorf("PENGUIN_V3_GLOBAL_MATRIX_INCREMENTAL=%q sets %q, want %q", value, set.GlobalMatrixIncremental, want)
		}
		if set.GlobalMatrixIncremental.Incremental() != (want != GlobalMatrixIncrementalOff) {
			t.Errorf("%q: Incremental() is %v", value, set.GlobalMatrixIncremental.Incremental())
		}
	}

	for _, value := range []string{"true", "1", "shadow", ""} {
		t.Setenv("PENGUIN_V3_GLOBAL_MATRIX_INCREMENTAL", value)
		var set ConfigSpec
		err := envconfig.Process("penguin_v3", &set)
		if value == "" {
			// set but empty: off
			if err != nil || set.GlobalMatrixIncremental != GlobalMatrixIncrementalOff {
				t.Errorf("an empty PENGUIN_V3_GLOBAL_MATRIX_INCREMENTAL gives %q, %v; want off", set.GlobalMatrixIncremental, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("PENGUIN_V3_GLOBAL_MATRIX_INCREMENTAL=%q was accepted as %q", value, set.GlobalMatrixIncremental)
		}
	}

	t.Setenv("PENGUIN_V3_GLOBAL_MATRIX_INCREMENTAL", "on")
	t.Setenv("PENGUIN_V3_GLOBAL_MATRIX_BASE_MAX_AGE", "6h")
	t.Setenv("PENGUIN_V3_GLOBAL_MATRIX_VERIFY_EVERY", "48")
	t.Setenv("PENGUIN_V3_GLOBAL_MATRIX_MAX_CHANGED_STAGES", "20")
	var set ConfigSpec
	if err := envconfig.Process("penguin_v3", &set); err != nil {
		t.Fatal(err)
	}
	if set.GlobalMatrixBaseMaxAge != 6*time.Hour || set.GlobalMatrixVerifyEvery != 48 || set.GlobalMatrixMaxChangedStages != 20 {
		t.Errorf("set to 6h, 48 and 20, got %v, %d and %d", set.GlobalMatrixBaseMaxAge, set.GlobalMatrixVerifyEvery, set.GlobalMatrixMaxChangedStages)
	}
}
