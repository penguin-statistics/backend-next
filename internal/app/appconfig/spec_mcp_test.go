package appconfig

import (
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// The MCP server settings are set by environment variables in the manifests: keep their names.
func TestMCPSettings(t *testing.T) {
	// the settings required without a default
	for _, key := range []string{"DROP_REPORT_ARCHIVE_S3_BUCKET", "DROP_REPORT_ARCHIVE_S3_REGION", "AWS_ACCESS_KEY", "AWS_SECRET_KEY", "POSTGRES_DSN"} {
		t.Setenv("PENGUIN_V3_"+key, "x")
	}

	var defaults ConfigSpec
	if err := envconfig.Process("penguin_v3", &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.MCPEnabled {
		t.Error("the MCP server is enabled by default")
	}
	if defaults.MCPRateLimitMax != 120 || defaults.MCPRateLimitWindow != time.Minute {
		t.Errorf("defaults: max %d, window %s", defaults.MCPRateLimitMax, defaults.MCPRateLimitWindow)
	}

	t.Setenv("PENGUIN_V3_MCP_ENABLED", "true")
	t.Setenv("PENGUIN_V3_MCP_RATE_LIMIT_MAX", "30")
	t.Setenv("PENGUIN_V3_MCP_RATE_LIMIT_WINDOW", "10s")
	var set ConfigSpec
	if err := envconfig.Process("penguin_v3", &set); err != nil {
		t.Fatal(err)
	}
	if !set.MCPEnabled || set.MCPRateLimitMax != 30 || set.MCPRateLimitWindow != 10*time.Second {
		t.Errorf("set: enabled %v, max %d, window %s", set.MCPEnabled, set.MCPRateLimitMax, set.MCPRateLimitWindow)
	}
}
