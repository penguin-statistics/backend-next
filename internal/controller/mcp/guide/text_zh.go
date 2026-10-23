package guide

// zh is the guide in Simplified Chinese. The labels of Cherry Studio are those of its Chinese interface; the labels of
// Claude and ChatGPT stay in English, with a gloss, as their Chinese interfaces were not checked.
var zh = &text{
	Lang:            "zh",
	Name:            "中文",
	ContentLanguage: "zh-CN",
	Title:           "企鹅物流数据统计 MCP 服务器",
	Description: "让 AI 助手直接查明日方舟掉率：免费、只读、无需注册和 API Key 的 MCP 服务器。介绍如何添加到 Cherry Studio、" +
		"Claude、ChatGPT、Cursor、VS Code 等应用。",
	MarkdownLink: "Markdown",
	Copy:         "复制",
	Copied:       "已复制",
	ForAgents:    "给 AI 智能体：本页的 Markdown 版在 {md} ，面向智能体的安装步骤在 {llms} 。",
	Lead: []string{
		"让你的 AI 助手直接查明日方舟掉率。企鹅物流数据统计（penguin-stats.io）汇总玩家上报的每一关掉落，覆盖国服、美服（国际服）、" +
			"日服、韩服。接入这个服务器后，Cherry Studio、Claude、ChatGPT、Cursor、VS Code 等 AI 应用就能替你查询最新的掉率和刷图效率。",
		"MCP（Model Context Protocol）是 AI 应用连接外部工具的标准方式。",
	},
	Badges: "免费 · 无需注册 · 无需 API Key · 只读",

	URLLabel: "服务器地址",
	Card: [][2]string{
		{"传输方式", "Streamable HTTP（不是 SSE）"},
		{"认证", "无。应用询问时，选择「无认证」「No authentication」或「No sign in」"},
		{"建议名称", "`penguin-stats`"},
	},
	URLNote: "请完整填写以 `/mcp` 结尾的地址。本页（{guide} ）是说明页，不是服务器地址。",

	OneClick:     "一键添加：",
	AddTo:        "添加到 %s",
	OneClickApps: []string{"cherry-studio", "claude", "cursor", "vscode"},
	OneClickNote: "这些链接只会打开对应应用并预填信息，最后仍需你确认。",

	AgentSummary: "让 AI 帮你装",
	AgentIntro:   "把下面这段话发给 Claude Code、Codex、Cursor 等编程助手：",
	AgentPrompt: "请为当前这个 AI 工具添加「企鹅物流数据统计」MCP 服务器：地址 {url} ，传输方式 Streamable HTTP，无需认证。按 " +
		"{llms} 中对应客户端的步骤，以用户（全局）范围添加，完成后列出它的工具，确认连接成功。",

	Install:     "安装",
	ChatApps:    "聊天应用",
	CodingTools: "编程工具",
	Chat:        []string{"cherry-studio", "claude", "chatgpt"},
	Coding:      []string{"cursor", "vscode", "claude-code", "codex", "trae", "gemini-cli", "cline"},
	Clients: map[string]clientText{
		"cherry-studio": {
			Summary: "Cherry Studio",
			Blocks: []block{
				para("一键添加：[添加到 Cherry Studio]({cherry-studio.link})（需要较新版本）。导入后服务器默认关闭，确认无误后手动开启。" +
					"点击没有反应时，请用下面的手动方式。"),
				para("手动添加：【设置】→【MCP】→【MCP 服务器】→【添加】→「从 JSON 导入」，粘贴："),
				snippet("streamable.json"),
				para("`\"type\": \"streamableHttp\"` 不能省略。省略时 Cherry Studio 会按旧的 SSE 方式连接，导致失败。"),
				para("启用：在【工作】→ Agent 菜单 →【编辑】→【MCP】中启用该服务器（以你的版本界面为准）。"),
			},
			Docs: "https://docs.cherryai.com.cn/advanced-basic/extensions/mcp",
		},
		"claude": {
			Summary: "Claude（网页版、桌面版、手机 App）",
			Blocks: []block{
				steps(
					"点击 [添加到 Claude]({claude.link})，或进入 **Customize → Connectors → + Add → Add custom connector**（自定义连接器）。",
					"名称填 `Penguin Statistics`，远程 MCP 服务器地址填 `{url}`，点 **Continue**。",
					"**Authentication**（认证）选 **No sign in**（无需登录），再点 **Add**。",
					"在对话中点 **+ → Connectors**，打开 Penguin Statistics。",
				),
				para("免费版可添加 1 个自定义连接器。Team / Enterprise 需由管理员在 **Organization settings → Connectors** 中添加，" +
					"成员再点 **Connect**。手机 App 不能直接添加：在网页版或桌面版添加后会自动同步。用同一账号登录的 Claude Code 也能使用。"),
			},
			Docs: "https://support.claude.com/zh-CN/articles/11175166",
		},
		"chatgpt": {
			Summary: "ChatGPT（网页版）",
			Blocks: []block{
				steps(
					"打开 https://chatgpt.com/plugins ，点 **+**，选择 **Add custom MCP server**（添加自定义 MCP 服务器）。",
					"名称填 `Penguin Statistics`，Server URL 填 `{url}`。",
					"Authentication 选 **No authentication**（无认证）。阅读风险提示后点 **I understand and want to continue**，再点 "+
						"**Create as a plugin**。",
					"安装该插件；在对话框输入 **@** 并选择 Penguin Statistics。",
				),
				para("能否使用取决于你的订阅和工作区设置。ChatGPT 桌面版使用 Codex 的配置，见下方 Codex。"),
			},
			Docs: "https://developers.openai.com/api/docs/guides/custom-mcp-server",
		},
		"cursor": {
			Summary: "Cursor",
			Blocks: []block{
				para("[添加到 Cursor]({cursor.link})，或在 `~/.cursor/mcp.json` 中加入："),
				snippet("mcpservers.json"),
			},
			Docs: "https://cursor.com/docs/mcp",
		},
		"vscode": {
			Summary: "VS Code（GitHub Copilot）",
			Blocks: []block{
				para("[添加到 VS Code]({vscode.link}) · [VS Code Insiders]({vscode-insiders.link})"),
				para("或在命令面板执行 **MCP: Add Server**，选择 **HTTP (HTTP or Server-Sent Events)**，粘贴地址，服务器 ID 填 " +
					"`penguin-stats`，然后选择 **Global**，出现提示时再选 **Copilot Global**。服务器会保存到 `~/.copilot/mcp-config.json`" +
					"（设置了 `COPILOT_HOME` 时为 `$COPILOT_HOME/mcp-config.json`），也可以手动编辑，顶层键是 `mcpServers`："),
				snippet("copilot.json"),
				para("或在终端运行下面的命令，它会把服务器加到用户配置（user profile）的 `mcp.json`："),
				snippet("vscode.sh"),
				para("`.vscode/mcp.json` 和用户配置的 `mcp.json` 为兼容而保留，使用 VS Code 格式：顶层键是 `servers`，不是 `mcpServers`："),
				snippet("vscode.json"),
			},
			Docs: "https://code.visualstudio.com/docs/agent-customization/mcp-servers",
		},
		"claude-code": {
			Summary: "Claude Code",
			Blocks: []block{
				plugin(para("推荐安装插件，它会同时装好服务器和技能（见下方「插件与技能」）：")),
				plugin(snippet("claude-plugin.sh")),
				plugin(para("或只添加服务器：")),
				snippet("claude-code.sh"),
				para("用 `claude mcp list` 或 `/mcp` 检查。项目内的 `.mcp.json` 必须写 `\"type\": \"http\"`："),
				snippet("claude-code.json"),
			},
			Docs: "https://code.claude.com/docs/en/mcp",
		},
		"codex": {
			Summary: "Codex（CLI、IDE 插件、ChatGPT 桌面版）",
			Blocks: []block{
				snippet("codex.sh"),
				para("或在 `~/.codex/config.toml` 中加入："),
				snippet("codex.toml"),
				para("IDE 插件和 ChatGPT 桌面版共用这个文件。在它们的界面中：**Settings → MCP servers → Add server → Streamable HTTP**，" +
					"粘贴地址，**Save** 后 **Restart**。"),
			},
			Docs: "https://learn.chatgpt.com/docs/extend/mcp",
		},
		"trae": {
			Summary: "Trae",
			Blocks: []block{
				para("设置 → MCP → 添加 → 手动添加（Create Manually），粘贴下面的内容后确认（菜单名以实际界面为准）："),
				snippet("mcpservers.json"),
			},
			Docs: "https://docs.trae.cn/ide/model-context-protocol",
		},
		"gemini-cli": {
			Summary: "Gemini CLI",
			Blocks: []block{
				snippet("gemini.sh"),
				para("手动编辑 `~/.gemini/settings.json` 时请用 `httpUrl`（在 Gemini 中 `url` 表示旧的 SSE 方式）："),
				snippet("gemini.json"),
			},
			Docs: "https://geminicli.com/docs/tools/mcp-server/",
		},
		"cline": {
			Summary: "Cline",
			Blocks: []block{
				para("**MCP Servers → Remote Servers**：名称 `penguin-stats`，填入地址，Transport Type 选 **Streamable HTTP**，点 " +
					"**Add Server**。在 `cline_mcp_settings.json` 中必须写明类型（默认是 SSE）："),
				snippet("streamable.json"),
				para("命令行：`{cline.sh}`"),
			},
			Docs: "https://docs.cline.bot/mcp/mcp-overview",
		},
	},
	Other: otherApps{
		Summary: "其他应用",
		Head:    [3]string{"应用", "位置", "填写内容"},
		Rows: [][3]string{
			{"GitHub Copilot CLI", "终端", "`{copilot.sh}`"},
			{"Windsurf / Devin Desktop（Cascade 智能体）", "`mcp_config.json`", "`{windsurf.json}`（`serverUrl` 或 `url`）"},
			{"Devin CLI、Devin Desktop（Devin Local 智能体）", "终端", "`{devin.sh}`"},
			{"Zed", "`settings.json`", "`{zed.json}`"},
			{"JetBrains AI Assistant", "Settings → Tools → AI Assistant → Model Context Protocol (MCP) → Add → HTTP",
				"`{mcpservers.json}`"},
			{"LM Studio 0.3.17+", "[添加到 LM Studio]({lmstudio.link})，或 Program → Install → Edit mcp.json", "`{mcpservers.json}`"},
			{"opencode", "`opencode.json`", "`{opencode.json}`"},
			{"Kiro", "`~/.kiro/settings/mcp.json`", "`{mcpservers.json}`"},
			{"Goose", "[添加到 Goose]({goose.link})，或 `goose configure` → Remote Extension (Streamable HTTP)", "服务器地址"},
			{"Warp", "Settings → Agents → MCP servers → + Add", "`{warp.json}`"},
			{"Raycast（Pro）", "「Install MCP Server」命令 → Transport 选 HTTP", "服务器地址"},
			{"一次装到多个应用（需 Node.js）", "终端", "`{add-mcp.sh}`"},
			{"其他应用", "", "远程 MCP 服务器，Streamable HTTP，填入地址，无需认证"},
			{"只支持本地（stdio）服务器的应用", "配置文件", "`{mcp-remote.json}`（需要 Node.js）"},
		},
	},
	Docs: "文档：",

	Try: section{ID: "try", Heading: "试一试", Blocks: []block{
		para("中文、英文、日文、韩文都可以问；物品的别名、外号、拼音也能识别。"),
		items(
			"国服固源岩去哪刷最划算？按每个固源岩的理智消耗列出前三个关卡。",
			"国服 1-7 现在掉什么？各自掉率多少？",
			"国服现在有哪些活动开放？活动关卡分别掉什么？",
			"对比国服和美服刷装置的最佳关卡。",
			"日服的数据最近一次更新是什么时候？",
			"写一个 Python 脚本，用企鹅物流的数据列出国服固源岩掉率最高的 3 个关卡，并打印数据时间和出处。",
		),
		para("**确认已接通：** 问「用企鹅物流查一下国服固源岩去哪刷最划算」。正常的回答会说明服务器和数据时间，并注明「数据来源：企鹅物流数据统计」。" +
			"如果助手没调用工具就直接回答，请明确说「用 Penguin Statistics 的工具查」。"),
	}},
	Plugin: section{ID: "plugin", Heading: "插件与技能（可选）", Blocks: []block{
		para("MCP 服务器给助手提供数据；`penguin-stats-api` 技能教它正确使用这些数据：说明服务器、优先参考样本大的关卡、注明数据时间和出处，" +
			"并能写出正确调用 REST API 的代码。插件会把两者一起装好。"),
		items(
			"Claude Code：`/plugin marketplace add penguin-statistics/agent-skills`，再 `/plugin install penguin-stats@penguin-statistics`",
			"Claude（网页版、桌面版）：**Customize → Plugins → Add → Add marketplace** → 填 `penguin-statistics/agent-skills`，"+
				"选中 **Penguin Statistics** 后点 **Add**，然后在插件的 **Connectors** 标签页添加并连接服务器"+
				"（无需登录；Team 和 Enterprise 方案由 Owner 添加连接器）",
			"Codex：`codex plugin marketplace add penguin-statistics/agent-skills`，再 `codex plugin add penguin-stats@penguin-statistics`",
			"Gemini CLI：`gemini extensions install https://github.com/penguin-statistics/agent-skills`",
			"VS Code：开启 `chat.plugins.enabled` 后执行 **Chat: Install Plugin From Source** → "+
				"`https://github.com/penguin-statistics/agent-skills`",
			"只装技能（任意 AI 编程工具）：`npx skills add penguin-statistics/agent-skills`",
		),
		para("如果之前手动添加过本服务器：Claude 和 Claude Code 会把它和插件里的服务器当作同一个，保留即可；" +
			"在其他应用里，请删除手动添加的那一项，否则工具可能重复加载。源代码：https://github.com/penguin-statistics/agent-skills"),
	}},

	ToolsHeading: "工具",
	ToolsIntro:   "全部只读：不会修改任何数据，也不能替你上报掉落。",
	ToolsHead:    [2]string{"工具", "作用"},
	Tools: []tool{
		{"search_items", "按中/英/日/韩名称、外号（如「狗粮」「石头」）、拼音或 ID 查找物品"},
		{"search_stages", "按关卡代号（1-7、S2-12）、关卡 ID、章节或活动名查找关卡"},
		{"get_item_drop_rates", "某物品在某服务器去哪刷：各关卡次数、掉率、单件理智"},
		{"get_stage_drops", "某关卡掉什么，以及各物品的掉率"},
		{"list_events", "某服务器正在开放或即将开放的活动及其关卡"},
		{"get_dataset_info", "数字的含义、各服务器数据的更新时间、注意事项、许可与署名方式"},
	},

	Data: section{ID: "data", Heading: "数据与许可", Blocks: []block{items(
		"数据来自玩家上报，按服务器分别统计：国服（CN）、美服（US，即国际服/英文服）、日服（JP）、韩服（KR）。每个回答都附带数据计算时间。",
		"掉率 = 掉落数量 ÷ 次数，可以超过 100%；次数少于 100 的掉率波动较大。",
		"许可：[CC BY-NC 4.0](https://creativecommons.org/licenses/by-nc/4.0/deed.zh-hans)，可用于非商业用途，需署名："+
			"`数据来源：企鹅物流数据统计（penguin-stats.io），CC BY-NC 4.0`（英文：`Data: Penguin Statistics (penguin-stats.io), CC BY-NC 4.0`）。"+
			"服务器会提示 AI 在回答中注明出处。",
	)}},
	Limits: section{ID: "limits", Heading: "用量限制", Blocks: []block{items(
		"每个 IP 每{window} {limit} 次请求（IPv6 按 /64 计）。一次提问通常只需 2–4 次请求。",
		"超出后服务器会返回「rate limited, retry after N s」，等待相应秒数后重试即可。",
		"Claude 网页版、ChatGPT 等云端助手从各自公司的服务器发起连接，可能与其他用户共用额度；遇到该提示请等一分钟。",
	)}},
	Privacy: section{ID: "privacy", Heading: "隐私", Blocks: []block{items(
		"无需账号、无需 Key、不使用 Cookie。本页不加载任何第三方脚本，没有统计代码。",
		"服务器只能收到 AI 助手发来的内容：工具名和参数（例如物品名），看不到你的对话内容。",
		"每次请求我们会记录：时间、IP 地址（及其所属的国家、城市和网络）、User-Agent、MCP 客户端名称和版本、调用的方法和工具名、结果和耗时。"+
			"不记录工具参数，但调用失败时，记录的错误信息可能引用出错的参数（例如不存在的物品名）。",
		"日志由我们的监控服务商（Datadog）保存 15 天后删除，仅用于运维、防止滥用和统计各工具与客户端的使用情况，不会出售，也不会用于广告。",
		"请求经过 Cloudflare，由 Cloudflare 按其自身条款处理。",
	)}},
	Troubleshooting: section{ID: "troubleshooting", Heading: "常见问题", Blocks: []block{
		items(
			"**连不上、提示 SSE 错误或 405：** 应用可能在用旧的 SSE 方式，本服务器不支持。请改为 Streamable HTTP：Cherry Studio 和 "+
				"Cline 写 `\"type\": \"streamableHttp\"`，Gemini CLI 用 `httpUrl`。在这几个应用里只写 `url` 就表示 SSE。",
			"**VS Code 里看不到服务器：** `~/.copilot/mcp-config.json` 和工作区的 `.mcp.json` 顶层键是 `mcpServers`；"+
				"`.vscode/mcp.json` 和用户配置的 `mcp.json` 顶层键是 `servers`。",
			"**要求登录或授权：** 本服务器无需认证，选「No sign in」或「No authentication」。",
			"**在浏览器打开 `/mcp` 跳到了本页：** 正常。这个地址只接受 AI 应用的 POST 请求。",
			"**提示 rate limited：** 见「用量限制」。",
			"**添加了但 AI 不调用：** 先在对话中启用（Claude：+ → Connectors；ChatGPT：输入 @ 选择；Cherry Studio：在 Agent 中启用 MCP），"+
				"再在提问里写明「用企鹅物流查」。",
			"**和网站上的数字不一样：** 先确认是同一个服务器（默认国服）和时间范围；可以让 AI 说明它用的服务器和数据时间。",
			"**中国大陆网络：** 本服务器暂无 .cn 镜像，本地运行的应用在大陆可能连接较慢或失败；Claude、ChatGPT 等云端助手从海外服务器连接，不受影响。",
		),
		para("**不借助 AI 测试：** 运行 `npx @modelcontextprotocol/inspector`，选择 Streamable HTTP，填入地址，Connect 后点 List Tools。或者："),
		snippet("curl.sh"),
	}},
	Links: section{ID: "links", Heading: "链接", Blocks: []block{items(
		"网站：{site}",
		"REST API 文档：https://developer.penguin-stats.io/public-api · OpenAPI：{site}/swagger/doc.json",
		"本页 Markdown 版：{md} · 给 AI 读的说明：{llms} · 全站说明：{site}/llms.txt",
		"Server card：{card}",
		"源代码：{repo} （internal/controller/mcp）",
		"反馈问题：{repo}/issues",
	)}},
	Minute: "分钟",

	Footer: "各应用的操作步骤最后核对于 2026-10-23。应用界面经常变化，如发现步骤过时，请[提交 issue]({repo}/issues)。",
}
