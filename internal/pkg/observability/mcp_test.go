package observability

import "testing"

func TestMCPLabels(t *testing.T) {
	for _, tc := range []struct {
		label func(string) string
		value string
		want  string
	}{
		{MCPMethodLabel, "tools/call", "tools/call"},
		{MCPMethodLabel, "server/discover", "server/discover"},
		{MCPMethodLabel, "notifications/initialized", "notifications/initialized"},
		{MCPMethodLabel, "tools/call/../../etc", "other"},
		{MCPMethodLabel, "", "other"},
		{MCPClientLabel, "claude-ai", "claude-ai"},
		{MCPClientLabel, "claude-code", "claude-code"},
		{MCPClientLabel, "openai-mcp", "chatgpt"},
		{MCPClientLabel, "cursor-vscode", "cursor"},
		{MCPClientLabel, "Visual Studio Code", "vscode"},
		{MCPClientLabel, "mcp-inspector", "inspector"},
		{MCPClientLabel, "my-agent", "other"},
		{MCPClientLabel, "", "unknown"},
	} {
		if got := tc.label(tc.value); got != tc.want {
			t.Errorf("label(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
