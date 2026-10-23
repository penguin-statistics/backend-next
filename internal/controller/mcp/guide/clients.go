package guide

import (
	"encoding/base64"
	"net/url"
	"path"
	"strings"
)

// snippets are the commands and configuration the guide gives, the same in every language, by name: the texts show
// one with its name in braces ({cursor.json}). Each has {url} for the URL of the server. The extension of a name is
// the language of the snippet (TestSnippets parses the .json ones). The keys and the transports are the apps' own
// (checked against their documentation on 2026-10-23): a wrong key is the commonest silent failure.
var snippets = map[string]string{
	// Cursor, Trae, Kiro, LM Studio and the JetBrains AI Assistant
	"mcpservers.json": `{"mcpServers":{"penguin-stats":{"url":"{url}"}}}`,
	// Cherry Studio and Cline take a url without a type for the old SSE transport
	"streamable.json":  `{"mcpServers":{"penguin-stats":{"type":"streamableHttp","url":"{url}"}}}`,
	"claude-code.sh":   "claude mcp add --transport http --scope user penguin-stats {url}",
	"claude-code.json": `{"mcpServers":{"penguin-stats":{"type":"http","url":"{url}"}}}`,
	// the shell form of /plugin marketplace add and /plugin install: it runs when pasted in a terminal, or by an agent
	"claude-plugin.sh": "claude plugin marketplace add penguin-statistics/agent-skills\n" +
		"claude plugin install penguin-stats@penguin-statistics",
	// VS Code's Global → Copilot Global, ~/.copilot/mcp-config.json: the portable format, mcpServers
	"copilot.json": `{"mcpServers":{"penguin-stats":{"type":"http","url":"{url}"}}}`,
	// writes the user-profile mcp.json
	"vscode.sh": `code --add-mcp '{"name":"penguin-stats","type":"http","url":"{url}"}'`,
	// the VS Code format of .vscode/mcp.json and the user-profile mcp.json: servers, not mcpServers
	"vscode.json": `{"servers":{"penguin-stats":{"type":"http","url":"{url}"}}}`,
	"codex.sh":    "codex mcp add penguin-stats --url {url}",
	"codex.toml":  "[mcp_servers.penguin-stats]\nurl = \"{url}\"",
	// the project is the default scope of Gemini CLI
	"gemini.sh": "gemini mcp add --transport http -s user penguin-stats {url}",
	// in Gemini CLI, url is the old SSE transport, httpUrl Streamable HTTP
	"gemini.json": `{"mcpServers":{"penguin-stats":{"httpUrl":"{url}"}}}`,
	"cline.sh":    "cline mcp add penguin-stats {url} --transport http --yes",
	"copilot.sh":  "copilot mcp add --transport http penguin-stats {url}",
	// Windsurf / Devin Desktop's Cascade agent takes serverUrl or url
	"windsurf.json": `{"mcpServers":{"penguin-stats":{"serverUrl":"{url}"}}}`,
	"devin.sh":      "devin mcp add -s user penguin-stats {url}",
	"zed.json":      `{"context_servers":{"penguin-stats":{"url":"{url}"}}}`,
	"opencode.json": `{"mcp":{"penguin-stats":{"type":"remote","url":"{url}","enabled":true}}}`,
	"warp.json":     `{"penguin-stats":{"url":"{url}"}}`,
	"add-mcp.sh":    "npx -y add-mcp {url} --name penguin-stats -g",
	// for apps that run local (stdio) servers only
	"mcp-remote.json": `{"mcpServers":{"penguin-stats":{"command":"npx","args":["-y","mcp-remote","{url}","--transport","http-only"]}}}`,
	"curl.sh": `curl -s {url} -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' ` +
		`-H 'Mcp-Protocol-Version: 2025-06-18' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'`,
}

// langOf is the language of the snippet name for Markdown, from its extension
func langOf(name string) string {
	return strings.TrimPrefix(path.Ext(name), ".")
}

// client is an app with a one-click link: a link that opens the app with the server filled in, for the user to
// review and confirm (Claude shows a notice, Cherry Studio adds the server switched off, VS Code asks for trust).
type client struct {
	// name is the name of the app, in the label of its link (Add to Cursor)
	name string
	// link returns the link for the server at endpoint
	link func(endpoint string) string
}

// clients are the apps with a one-click link, by the id of their section in the guide; the texts show the link of
// one as {<id>.link}. Only the formats the apps document or that were checked in their source are used, and the web
// links (cursor.com, vscode.dev) rather than custom schemes where an app has both, which work wherever the app is
// installed.
var clients = map[string]client{
	// https://claude.com/docs/connectors/building/directory-vs-custom
	"claude": {name: "Claude", link: func(endpoint string) string {
		return "https://claude.ai/customize/connectors?modal=add-custom-connector&connectorName=Penguin%20Statistics&connectorUrl=" +
			url.QueryEscape(endpoint)
	}},
	// https://cursor.com/docs/mcp/install-links: the configuration in base64
	"cursor": {name: "Cursor", link: func(endpoint string) string {
		return "https://cursor.com/en/install-mcp?name=penguin-stats&config=" + url.QueryEscape(base64JSON(`{"url":"{url}"}`, endpoint))
	}},
	// the redirector of vscode.dev to vscode:mcp/install, with the configuration as JSON
	// (https://code.visualstudio.com/api/extension-guides/ai/mcp)
	"vscode": {name: "VS Code", link: vscodeLink},
	"vscode-insiders": {name: "VS Code Insiders", link: func(endpoint string) string {
		return vscodeLink(endpoint) + "&quality=insiders"
	}},
	// Cherry Studio v2.1.7 (src/main/services/protocol/handlers/mcpInstall.ts): a map of servers, as in its JSON
	// import, in base64 in the parameter servers
	"cherry-studio": {name: "Cherry Studio", link: func(endpoint string) string {
		return "cherrystudio://mcp/install?servers=" + url.QueryEscape(base64JSON(snippets["streamable.json"], endpoint))
	}},
	// the format of Cursor
	"lmstudio": {name: "LM Studio", link: func(endpoint string) string {
		return "https://lmstudio.ai/install-mcp?name=penguin-stats&config=" + url.QueryEscape(base64JSON(`{"url":"{url}"}`, endpoint))
	}},
	// https://goose-docs.ai/docs/getting-started/using-extensions
	"goose": {name: "Goose", link: func(endpoint string) string {
		return "goose://extension?url=" + url.QueryEscape(endpoint) +
			"&type=streamable_http&id=penguin-stats&name=Penguin%20Statistics&description=Arknights%20drop%20rates"
	}},
}

func vscodeLink(endpoint string) string {
	return "https://vscode.dev/redirect/mcp/install?name=penguin-stats&config=" +
		url.QueryEscape(strings.ReplaceAll(`{"type":"http","url":"{url}"}`, "{url}", endpoint))
}

// base64JSON is the JSON json, with endpoint for {url}, in base64
func base64JSON(json, endpoint string) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(json, "{url}", endpoint)))
}
