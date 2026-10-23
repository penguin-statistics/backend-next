package guide

var en = &text{
	Lang:            "en",
	Name:            "English",
	ContentLanguage: "en",
	Title:           "Penguin Statistics MCP Server",
	Description: "Arknights drop rates for your AI assistant: a free, read-only MCP server without account or API key. " +
		"How to add it to Claude, ChatGPT, Cursor, VS Code, Cherry Studio and other apps.",
	MarkdownLink: "Markdown",
	Copy:         "Copy",
	Copied:       "Copied",
	ForAgents:    "For AI agents: this page is at {md} as Markdown, and the setup steps for agents are at {llms}.",
	Lead: []string{
		"Arknights drop rates for your AI assistant. Penguin Statistics (penguin-stats.io) counts the drops players " +
			"report for every stage on the CN, US, JP and KR servers. Connect this server and Claude, ChatGPT, Cursor, " +
			"VS Code, Cherry Studio and other AI apps can look the numbers up for you.",
		"MCP (Model Context Protocol) is the standard way AI apps connect to outside tools.",
	},
	Badges: "Free · No account · No API key · Read-only",

	URLLabel: "Server URL",
	Card: [][2]string{
		{"Transport", "Streamable HTTP (not SSE)"},
		{"Authentication", `None. If your app asks, choose "No authentication" or "No sign in".`},
		{"Suggested name", "`penguin-stats`"},
	},
	URLNote: "Use the URL exactly as shown, ending in `/mcp`. This page ({guide}) is the guide, not the server.",

	OneClick:     "One click:",
	AddTo:        "Add to %s",
	OneClickApps: []string{"claude", "cursor", "vscode", "cherry-studio"},
	OneClickNote: "These open your app with the server filled in. You still review and confirm.",

	AgentSummary: "Let your agent set it up",
	AgentIntro:   "Paste this into Claude Code, Codex, Cursor or another coding agent:",
	AgentPrompt: "Add the Penguin Statistics MCP server to this agent: URL {url}, transport Streamable HTTP, no " +
		"authentication. Follow the steps for this client in {llms}, add it at user (global) scope, then list its " +
		"tools to confirm it works.",

	Install:     "Install",
	ChatApps:    "Chat apps",
	CodingTools: "Coding tools",
	Chat:        []string{"claude", "chatgpt", "cherry-studio"},
	Coding:      []string{"claude-code", "cursor", "vscode", "codex", "gemini-cli", "trae", "cline"},
	Clients: map[string]clientText{
		"claude": {
			Summary: "Claude (claude.ai, Claude Desktop, Claude mobile)",
			Blocks: []block{
				steps(
					"Open [Add to Claude]({claude.link}), or go to **Customize → Connectors → + Add → Add custom connector**.",
					"Name: `Penguin Statistics`. Remote MCP server URL: `{url}`. Click **Continue**.",
					"Under **Authentication** choose **No sign in**, then **Add**.",
					"In a chat, click **+ → Connectors** and turn on Penguin Statistics.",
				),
				para("The Free plan allows one custom connector. On Team and Enterprise plans an owner adds it under " +
					"**Organization settings → Connectors**, then members click **Connect**. The mobile apps can't add " +
					"connectors; add it on the web or desktop and it syncs. Claude Code signed in with the same account " +
					"can use it too."),
			},
			Docs: "https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp",
		},
		"chatgpt": {
			Summary: "ChatGPT (web)",
			Blocks: []block{
				steps(
					"Go to https://chatgpt.com/plugins, click **+**, then **Add custom MCP server**.",
					"Name `Penguin Statistics`, Server URL `{url}`.",
					"Authentication: **No authentication**. Read the warning, click **I understand and want to continue**, "+
						"then **Create as a plugin**.",
					"Install the plugin. In a chat, type **@** and choose Penguin Statistics.",
				),
				para("Availability depends on your plan and workspace settings. The ChatGPT desktop app uses the Codex " +
					"settings: see Codex below."),
			},
			Docs: "https://developers.openai.com/api/docs/guides/custom-mcp-server",
		},
		"cherry-studio": {
			Summary: "Cherry Studio",
			Blocks: []block{
				para("One click: [Add to Cherry Studio]({cherry-studio.link}). It needs a recent version, and the server " +
					"arrives switched off: review it, then turn it on. If nothing happens, add it by hand."),
				para("By hand: **Settings → MCP → MCP Servers → Add → Import from JSON**, and paste:"),
				snippet("streamable.json"),
				para("Keep `\"type\": \"streamableHttp\"`. Without it Cherry Studio uses the old SSE transport, and " +
					"the connection fails."),
				para("Then enable the server for your agent (**Work → Agent → Edit → MCP**; the menus may differ in your " +
					"version)."),
			},
			Docs: "https://docs.cherryai.com.cn/advanced-basic/extensions/mcp",
		},
		"claude-code": {
			Summary: "Claude Code",
			Blocks: []block{
				plugin(para("Recommended: the plugin, which adds the server and the skill (see Plugin and skill below):")),
				plugin(snippet("claude-plugin.sh")),
				plugin(para("Or the server alone:")),
				snippet("claude-code.sh"),
				para("Check with `claude mcp list` or `/mcp`. In a project `.mcp.json`, keep `\"type\": \"http\"`:"),
				snippet("claude-code.json"),
			},
			Docs: "https://code.claude.com/docs/en/mcp",
		},
		"cursor": {
			Summary: "Cursor",
			Blocks: []block{
				para("[Add to Cursor]({cursor.link}), or add this to `~/.cursor/mcp.json`:"),
				snippet("mcpservers.json"),
			},
			Docs: "https://cursor.com/docs/mcp",
		},
		"vscode": {
			Summary: "VS Code (GitHub Copilot)",
			Blocks: []block{
				para("[Add to VS Code]({vscode.link}) · [VS Code Insiders]({vscode-insiders.link})"),
				para("Or run **MCP: Add Server** from the Command Palette, choose **HTTP (HTTP or Server-Sent Events)**, " +
					"paste the URL, enter `penguin-stats` as the server ID, then select **Global** and, when prompted, " +
					"**Copilot Global**. This saves the server in `~/.copilot/mcp-config.json` " +
					"(`$COPILOT_HOME/mcp-config.json` if `COPILOT_HOME` is set), which you can also edit by hand. Its " +
					"top-level key is `mcpServers`:"),
				snippet("copilot.json"),
				para("Or run this, which adds the server to your user-profile `mcp.json`:"),
				snippet("vscode.sh"),
				para("`.vscode/mcp.json` and the user-profile `mcp.json`, kept for compatibility, use the VS Code format: " +
					"the top-level key is `servers`, not `mcpServers`:"),
				snippet("vscode.json"),
			},
			Docs: "https://code.visualstudio.com/docs/agent-customization/mcp-servers",
		},
		"codex": {
			Summary: "Codex (CLI, IDE extension, ChatGPT desktop app)",
			Blocks: []block{
				snippet("codex.sh"),
				para("Or add to `~/.codex/config.toml`:"),
				snippet("codex.toml"),
				para("The IDE extension and the ChatGPT desktop app share this file. In their UI: **Settings → MCP " +
					"servers → Add server → Streamable HTTP**, paste the URL, **Save**, then **Restart**."),
			},
			Docs: "https://learn.chatgpt.com/docs/extend/mcp",
		},
		"gemini-cli": {
			Summary: "Gemini CLI",
			Blocks: []block{
				snippet("gemini.sh"),
				para("Or in `~/.gemini/settings.json` use `httpUrl` (in Gemini, `url` means the old SSE transport):"),
				snippet("gemini.json"),
			},
			Docs: "https://geminicli.com/docs/tools/mcp-server/",
		},
		"trae": {
			Summary: "Trae",
			Blocks: []block{
				para("**Settings → MCP → Add → Create Manually**, paste the following, then **Confirm**:"),
				snippet("mcpservers.json"),
			},
			Docs: "https://docs.trae.ai/ide/model-context-protocol",
		},
		"cline": {
			Summary: "Cline",
			Blocks: []block{
				para("**MCP Servers → Remote Servers**: name `penguin-stats`, the URL, Transport Type **Streamable HTTP** " +
					"→ **Add Server**. In `cline_mcp_settings.json` set the type explicitly (the default is SSE):"),
				snippet("streamable.json"),
				para("CLI: `{cline.sh}`"),
			},
			Docs: "https://docs.cline.bot/mcp/mcp-overview",
		},
	},
	Other: otherApps{
		Summary: "Other apps",
		Head:    [3]string{"App", "Where", "What to add"},
		Rows: [][3]string{
			{"GitHub Copilot CLI", "shell", "`{copilot.sh}`"},
			{"Windsurf / Devin Desktop (Cascade agent)", "`mcp_config.json`", "`{windsurf.json}` (`serverUrl` or `url`)"},
			{"Devin CLI, Devin Desktop (Devin Local agent)", "shell", "`{devin.sh}`"},
			{"Zed", "`settings.json`", "`{zed.json}`"},
			{"JetBrains AI Assistant", "Settings → Tools → AI Assistant → Model Context Protocol (MCP) → Add → HTTP",
				"`{mcpservers.json}`"},
			{"LM Studio 0.3.17+", "[Add to LM Studio]({lmstudio.link}), or Program → Install → Edit mcp.json", "`{mcpservers.json}`"},
			{"opencode", "`opencode.json`", "`{opencode.json}`"},
			{"Kiro", "`~/.kiro/settings/mcp.json`", "`{mcpservers.json}`"},
			{"Goose", "[Add to Goose]({goose.link}), or `goose configure` → Remote Extension (Streamable HTTP)", "the URL"},
			{"Warp", "Settings → Agents → MCP servers → + Add", "`{warp.json}`"},
			{"Raycast (Pro)", `"Install MCP Server" command → Transport HTTP`, "the URL"},
			{"Several apps at once (Node.js)", "shell", "`{add-mcp.sh}`"},
			{"Anything else", "", "Remote MCP server, Streamable HTTP, the URL, no authentication"},
			{"Apps that only run local (stdio) servers", "config", "`{mcp-remote.json}` (needs Node.js)"},
		},
	},
	Docs: "Docs: ",

	Try: section{ID: "try", Heading: "Try it", Blocks: []block{
		para("Ask in any language: 中文, English, 日本語, 한국어. Names, nicknames and pinyin all work."),
		items(
			"Where should I farm Orirock Cube on the US (global) server? Top 3 stages by sanity per item.",
			"What does 1-7 drop on CN, and at what rates?",
			"Which events are open on CN right now, and what do their stages drop?",
			"Compare the best stages for Device on CN and US.",
			"When was the JP data last updated?",
			"Write a Python script that prints the 3 CN stages with the highest Orirock Cube drop rate, with the data "+
				"time and the credit line.",
		),
		para(`**Check that it works:** ask "Using Penguin Statistics, where is Orirock Cube cheapest to farm on CN?" ` +
			"A good answer names the server, says when the data is from, and credits Penguin Statistics. If the " +
			`assistant answers without calling a tool, say "use the Penguin Statistics tools".`),
	}},
	Plugin: section{ID: "plugin", Heading: "Plugin and skill (optional)", Blocks: []block{
		para("The MCP server gives your assistant the data. The `penguin-stats-api` skill teaches it to use the data " +
			"well: name the server, prefer large samples, give the data time and the credit, and write correct code " +
			"against the REST API. The plugin installs both."),
		items(
			"Claude Code: `/plugin marketplace add penguin-statistics/agent-skills`, then "+
				"`/plugin install penguin-stats@penguin-statistics`",
			"Claude (web, desktop): **Customize → Plugins → Add → Add marketplace** → `penguin-statistics/agent-skills`, "+
				"select **Penguin Statistics** and click **Add**, then on the plugin's **Connectors** tab add the server "+
				"and connect it (no sign-in; on Team and Enterprise plans an Owner adds the connector)",
			"Codex: `codex plugin marketplace add penguin-statistics/agent-skills`, then "+
				"`codex plugin add penguin-stats@penguin-statistics`",
			"Gemini CLI: `gemini extensions install https://github.com/penguin-statistics/agent-skills`",
			"VS Code: **Chat: Install Plugin From Source** → `https://github.com/penguin-statistics/agent-skills` "+
				"(with `chat.plugins.enabled` on)",
			"Skill only, any agent: `npx skills add penguin-statistics/agent-skills`",
		),
		para("If you added the server by hand before: Claude and Claude Code treat it and the plugin's server as one, " +
			"so keep it. In the other apps, remove the entry you added, or the tools may load twice. " +
			"Source: https://github.com/penguin-statistics/agent-skills"),
	}},

	ToolsHeading: "Tools",
	ToolsIntro:   "All tools are read-only. They cannot change anything or submit drop reports.",
	ToolsHead:    [2]string{"Tool", "What it does"},
	Tools: []tool{
		{"search_items", "Finds items by name in zh, en, ja or ko, by nickname (狗粮, 石头), pinyin or ID"},
		{"search_stages", "Finds stages by code (1-7, S2-12), stage ID, or chapter or event name"},
		{"get_item_drop_rates", "Where to farm an item on a server: runs, drop rate, sanity per item"},
		{"get_stage_drops", "What a stage drops, with each item's rate"},
		{"list_events", "Events open or upcoming on a server, with their stages"},
		{"get_dataset_info", "What the numbers mean, how fresh each server's data is, caveats, license and credit"},
	},

	Data: section{ID: "data", Heading: "Data and license", Blocks: []block{items(
		"Players report their drops. The numbers are counted per server: CN, US (the global English server), JP and KR. "+
			"Each answer includes when the data was calculated.",
		"Drop rate = quantity ÷ runs. It can exceed 100%. Rates from fewer than 100 runs are noisy.",
		"License: [CC BY-NC 4.0](https://creativecommons.org/licenses/by-nc/4.0/), for non-commercial use with credit: "+
			"`Data: Penguin Statistics (penguin-stats.io), CC BY-NC 4.0`. The server asks assistants to include it.",
	)}},
	Limits: section{ID: "limits", Heading: "Limits", Blocks: []block{items(
		"{limit} requests per {window} per IP address (IPv6: per /64). A question usually takes 2 to 4 requests.",
		`Over the limit, the server answers "rate limited, retry after N s". Wait that long and retry.`,
		"Claude.ai and ChatGPT connect from their companies' servers, so their users may share a limit. If you see "+
			"this message there, wait a minute.",
	)}},
	Privacy: section{ID: "privacy", Heading: "Privacy", Blocks: []block{items(
		"No account, no key, no cookies. This page loads no scripts from other sites and has no analytics.",
		"The server only receives what your assistant sends it: the tool name and its arguments, such as an item name. "+
			"It never sees your conversation.",
		"For each request we log the time, the IP address (with the country, city and network it belongs to), the "+
			"User-Agent, the MCP client's name and version, the method and tool name, the result and how long it took. "+
			"We do not log tool arguments, but when a call fails, the logged error message can quote the argument that "+
			"was wrong, such as an unknown item name.",
		"Logs are kept for 15 days by our monitoring provider (Datadog), then deleted. We use them only to run the "+
			"service, stop abuse and count which tools and clients are used. Nothing is sold or shared for advertising.",
		"Requests pass through Cloudflare, which processes them under its own terms.",
	)}},
	Troubleshooting: section{ID: "troubleshooting", Heading: "Troubleshooting", Blocks: []block{
		items(
			`**"Can't connect", an SSE error, or 405:** the app may be using the old SSE transport, which this server `+
				"does not support. Choose Streamable HTTP: `\"type\": \"streamableHttp\"` in Cherry Studio and Cline, "+
				"`httpUrl` in Gemini CLI. In these apps a `url` alone means SSE.",
			"**The server doesn't appear in VS Code:** the top-level key is `mcpServers` in `~/.copilot/mcp-config.json` "+
				"and a workspace `.mcp.json`, but `servers` in `.vscode/mcp.json` and the user-profile `mcp.json`.",
			`**The app asks you to sign in:** no sign-in is needed. Choose "No sign in" or "No authentication".`,
			"**A 405 error when you open `/mcp` in a browser:** that's expected. The endpoint only takes POST requests "+
				"from AI apps; this page is the one to open.",
			`**"rate limited":** see Limits.`,
			"**The tools are added but not used:** turn the connector on in the chat (Claude: + → Connectors; ChatGPT: "+
				`type @; Cherry Studio: enable MCP for the agent) and say "use Penguin Statistics".`,
			"**The numbers differ from the website:** check the server (CN is the default) and the time range, and ask "+
				"the assistant which server and data time it used.",
			"**From mainland China:** there is no .cn mirror of this server yet. Local apps may be slow or fail to "+
				"connect; Claude and ChatGPT connect from their own servers and are not affected.",
		),
		para("**Test without an AI:** run `npx @modelcontextprotocol/inspector`, choose Streamable HTTP, enter the URL, " +
			"Connect, List Tools. Or:"),
		snippet("curl.sh"),
	}},
	Links: section{ID: "links", Heading: "Links", Blocks: []block{items(
		"Website: {site}",
		"REST API docs: https://developer.penguin-stats.io/public-api · OpenAPI: {site}/swagger/doc.json",
		"This guide as Markdown: {md} · for agents: {llms} · site guide: {site}/llms.txt",
		"Server card: {card}",
		"Source code: {repo} (internal/controller/mcp)",
		"Report a problem: {repo}/issues",
	)}},
	Minute: "minute",

	Footer: "Steps last checked against each app's documentation on 2026-10-23. Apps change their menus often; if a " +
		"step is out of date, please [open an issue]({repo}/issues).",

	LLMsPreface: []string{
		"> Read-only remote MCP server for Arknights drop statistics (CN, US, JP, KR servers). URL: {url} · Streamable " +
			"HTTP · no authentication · {toolcount} tools. Data: CC BY-NC 4.0, credit \"Data: Penguin Statistics " +
			"(penguin-stats.io), CC BY-NC 4.0\".",
		"If you are an AI agent asked to connect this server: use the exact URL above (it ends in /mcp), pick the " +
			"section for the client you run in, add it at user/global scope, then list the tools ({tools}) to verify. " +
			"Item and stage names work in Chinese, English, Japanese and Korean, including nicknames and pinyin.",
	},
}
