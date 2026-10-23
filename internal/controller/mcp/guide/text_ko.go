package guide

// ko is the guide in Korean. The labels of Claude's connectors are those of its Korean interface, as Claude's Korean
// help article on custom connectors (support.claude.com/ko/articles/11175166) gives them, with the English ones in
// parentheses. So is 사용자 정의 (Customize) in the steps of its plugins, whose other labels no Korean documentation
// shows and which stay in English. The labels of the other apps stay in English, with a gloss where it helps: Cherry
// Studio has no Korean interface, and the Korean interfaces of the others were not checked. The terms (드랍률,
// 표본 수, 아이템당 필요 이성, 이성, 라이센스) and the names of the servers are those of the website
// (internal/controller/seo/texts.go, frontend-v2's ko_KR.json), the names of the items those of the API (name_i18n).
var ko = &text{
	Lang:            "ko",
	Name:            "한국어",
	Abbr:            "한",
	ContentLanguage: "ko",
	Title:           "펭귄 물류 데이터 분석 부서 MCP 서버",
	Description: "AI 어시스턴트에서 명일방주 드랍률을 바로 확인하세요. 계정도 API 키도 필요 없는 무료 읽기 전용 " +
		"MCP 서버입니다. Claude, ChatGPT, Cursor, VS Code, Cherry Studio 등의 앱에 추가하는 방법을 안내합니다.",
	MarkdownLink: "Markdown",
	Copy:         "복사",
	Copied:       "복사됨",
	SkipLink:     "본문으로 건너뛰기",
	PageNav:      "이 페이지의 내용",
	LangNav:      "언어",
	AppNav:       "앱",
	Terminal:     "터미널",
	ForAgents:    "AI 에이전트용 안내: 이 페이지의 Markdown 버전은 {md} 에서, 에이전트용 설치 절차는 {llms} 에서 볼 수 있습니다.",
	Lead: []string{
		"AI 어시스턴트에서 바로 확인하는 명일방주 드랍률. 펭귄 물류 데이터 분석 부서(penguin-stats.io)는 " +
			"CN, US, JP, KR 서버의 모든 스테이지에서 플레이어가 보고한 드랍을 집계합니다. 이 MCP 서버를 연결하면 " +
			"Claude, ChatGPT, Cursor, VS Code, Cherry Studio 등의 AI 앱이 수치를 대신 찾아 줍니다.",
		"MCP(Model Context Protocol)는 AI 앱이 외부 도구와 연결하는 표준 방식입니다.",
	},
	Badges: "무료 · 계정 불필요 · API 키 불필요 · 읽기 전용",

	URLLabel: "서버 URL",
	Card: [][2]string{
		{"전송 방식", "Streamable HTTP (SSE 아님)"},
		{"인증", `없음. 앱에서 인증 방식을 물으면 "로그인 없음"(No sign in) 또는 "No authentication"(인증 없음)을 ` +
			`선택하세요.`},
		{"권장 이름", "`penguin-stats`"},
	},
	URLNote: "`/mcp`로 끝나는 위 URL을 그대로 사용하세요. 지금 보고 있는 {guide} 페이지는 안내용이며, " +
		"서버 URL이 아닙니다.",

	OneClick:     "원클릭 추가:",
	AddTo:        "%s에 추가",
	OneClickApps: []string{"claude", "cursor", "vscode", "cherry-studio"},
	OneClickNote: "서버 정보가 입력된 상태로 앱이 열립니다. 확인과 승인은 직접 해야 합니다.",

	AgentSummary: "에이전트에게 설치 맡기기",
	AgentIntro:   "Claude Code, Codex, Cursor 등의 코딩 에이전트에 다음 내용을 붙여넣으세요:",
	AgentPrompt: "이 에이전트에 Penguin Statistics MCP 서버를 추가해 주세요. URL은 {url}, 전송 방식은 Streamable " +
		"HTTP이고, 인증은 필요 없습니다. {llms} 문서에서 이 클라이언트에 해당하는 절차를 따라 사용자(전역) 범위로 " +
		"추가한 다음, 도구 목록을 조회해 정상적으로 동작하는지 확인해 주세요.",
	AgentCopy: "에이전트용 프롬프트 복사",
	AgentHint: "Claude Code, Codex, Cursor 등의 코딩 에이전트에 붙여넣으세요.",
	AgentMore: "전체 보기",
	AgentLess: "접기",

	Install:     "설치",
	ChatApps:    "채팅 앱",
	CodingTools: "코딩 도구",
	Chat:        []string{"claude", "chatgpt", "cherry-studio"},
	Coding:      []string{"claude-code", "cursor", "vscode", "codex", "gemini-cli", "trae", "cline"},
	Clients: map[string]clientText{
		"claude": {
			Summary: "Claude (claude.ai, Claude Desktop, Claude 모바일 앱)",
			Blocks: []block{
				steps(
					"[Claude에 추가]({claude.link})를 열거나, **사용자 정의 → 커넥터 → + 추가 → 커스텀 커넥터 추가**"+
						"(Customize → Connectors → + Add → Add custom connector)로 이동하세요.",
					"이름은 `Penguin Statistics`, 원격 MCP 서버 URL은 `{url}`로 입력하고 **계속**(Continue)을 클릭하세요.",
					"**인증**(Authentication)에서 **로그인 없음**(No sign in)을 선택한 다음 **추가**(Add)를 클릭하세요.",
					"채팅에서 **+ → 커넥터**(Connectors)를 클릭하고 Penguin Statistics를 켜세요.",
				),
				para("Free 플랜에서는 커스텀 커넥터를 1개만 추가할 수 있습니다. Team 및 Enterprise 플랜에서는 Owner가 " +
					"**조직 설정 → 커넥터**(Organization settings → Connectors)에서 추가하고, 구성원은 각자 **연결**(Connect)을 " +
					"클릭합니다. 모바일 앱에서는 커넥터를 추가할 수 없습니다. 웹이나 데스크톱 앱에서 추가하면 모바일 " +
					"앱에도 동기화됩니다. 같은 계정으로 로그인한 Claude Code에서도 사용할 수 있습니다."),
			},
			Docs: "https://support.claude.com/ko/articles/11175166",
		},
		"chatgpt": {
			Summary: "ChatGPT (웹)",
			Blocks: []block{
				steps(
					"https://chatgpt.com/plugins 페이지에서 **+** 버튼을 클릭한 다음 **Add custom MCP server**"+
						"(커스텀 MCP 서버 추가)를 클릭하세요.",
					"Name은 `Penguin Statistics`, Server URL은 `{url}`로 입력하세요.",
					"Authentication에서 **No authentication**(인증 없음)을 선택하세요. 경고를 읽고 "+
						"**I understand and want to continue**를 클릭한 다음 **Create as a plugin**(플러그인으로 생성)을 "+
						"클릭하세요.",
					"플러그인을 설치하세요. 채팅에서 **@** 기호를 입력하고 Penguin Statistics를 선택하세요.",
				),
				para("사용 가능 여부는 플랜과 워크스페이스 설정에 따라 다릅니다. ChatGPT 데스크톱 앱은 Codex 설정을 " +
					"사용하므로 아래 Codex 항목을 참고하세요."),
			},
			Docs: "https://developers.openai.com/api/docs/guides/custom-mcp-server",
		},
		"cherry-studio": {
			Summary: "Cherry Studio",
			Blocks: []block{
				para("원클릭 추가: [Cherry Studio에 추가]({cherry-studio.link}). 비교적 최근 버전이 필요하며, 서버는 " +
					"꺼진 상태로 추가됩니다. 내용을 확인한 뒤 켜세요. 아무 반응이 없으면 직접 추가하세요."),
				para("직접 추가: **Settings → MCP → MCP Servers → Add → Import from JSON**(JSON에서 가져오기)를 " +
					"선택하고 다음 내용을 붙여넣으세요:"),
				snippet("streamable.json"),
				para("`\"type\": \"streamableHttp\"`는 빼지 마세요. 이 항목이 없으면 Cherry Studio가 예전 SSE 전송 " +
					"방식을 사용해 연결에 실패합니다."),
				para("그다음 에이전트에서 서버를 활성화하세요: **Work → Agent → Edit → MCP**. 메뉴는 버전에 따라 " +
					"다를 수 있습니다."),
			},
			Docs: "https://docs.cherryai.com.cn/advanced-basic/extensions/mcp",
		},
		"claude-code": {
			Summary: "Claude Code",
			Blocks: []block{
				plugin(para("권장: 플러그인을 설치하면 서버와 스킬이 함께 추가됩니다(아래 '플러그인과 스킬' 참고):")),
				plugin(snippet("claude-plugin.sh")),
				plugin(para("서버만 추가하려면:")),
				snippet("claude-code.sh"),
				para("`claude mcp list` 또는 `/mcp`로 확인하세요. 프로젝트의 `.mcp.json`에는 `\"type\": \"http\"`를 " +
					"반드시 넣으세요:"),
				snippet("claude-code.json"),
			},
			Docs: "https://code.claude.com/docs/ko/mcp",
		},
		"cursor": {
			Summary: "Cursor",
			Blocks: []block{
				para("[Cursor에 추가]({cursor.link})를 클릭하거나 `~/.cursor/mcp.json`에 다음 내용을 추가하세요:"),
				snippet("mcpservers.json"),
			},
			Docs: "https://cursor.com/docs/mcp",
		},
		"vscode": {
			Summary: "VS Code (GitHub Copilot)",
			Blocks: []block{
				para("[VS Code에 추가]({vscode.link}) · [VS Code Insiders]({vscode-insiders.link})"),
				para("또는 Command Palette(명령 팔레트)에서 **MCP: Add Server**를 실행하고 " +
					"**HTTP (HTTP or Server-Sent Events)** 항목을 선택한 뒤 URL을 붙여넣고, 서버 ID에 `penguin-stats`를 " +
					"입력하세요. 그다음 **Global**을 선택하고, 물어보면 **Copilot Global**을 선택하세요. 서버는 " +
					"`~/.copilot/mcp-config.json`(`COPILOT_HOME`이 설정되어 있으면 `$COPILOT_HOME/mcp-config.json`)에 " +
					"저장되며, 이 파일을 직접 편집해도 됩니다. 최상위 키는 `mcpServers`입니다:"),
				snippet("copilot.json"),
				para("또는 다음 명령을 실행하면 사용자 프로필의 `mcp.json`에 서버가 추가됩니다:"),
				snippet("vscode.sh"),
				para("호환성을 위해 유지되는 `.vscode/mcp.json`과 사용자 프로필의 `mcp.json`은 VS Code 형식을 " +
					"사용합니다. 최상위 키가 `mcpServers`가 아니라 `servers`입니다:"),
				snippet("vscode.json"),
			},
			Docs: "https://code.visualstudio.com/docs/agent-customization/mcp-servers",
		},
		"codex": {
			Summary: "Codex (CLI, IDE 확장, ChatGPT 데스크톱 앱)",
			Blocks: []block{
				snippet("codex.sh"),
				para("또는 `~/.codex/config.toml`에 다음 내용을 추가하세요:"),
				snippet("codex.toml"),
				para("IDE 확장과 ChatGPT 데스크톱 앱도 이 파일을 함께 사용합니다. 앱 화면에서는 **Settings → MCP " +
					"servers → Add server → Streamable HTTP**를 선택하고 URL을 붙여넣은 뒤 **Save**(저장), " +
					"**Restart**(재시작)을 차례로 클릭하세요."),
			},
			Docs: "https://learn.chatgpt.com/docs/extend/mcp",
		},
		"gemini-cli": {
			Summary: "Gemini CLI",
			Blocks: []block{
				snippet("gemini.sh"),
				para("또는 `~/.gemini/settings.json`에 추가하세요. 이때는 `url`이 아니라 `httpUrl`을 사용하세요" +
					"(Gemini에서 `url`은 예전 SSE 전송 방식을 뜻합니다):"),
				snippet("gemini.json"),
			},
			Docs: "https://geminicli.com/docs/tools/mcp-server/",
		},
		"trae": {
			Summary: "Trae",
			Blocks: []block{
				para("**Settings → MCP → Add → Create Manually**(직접 추가)를 선택하고 다음 내용을 붙여넣은 뒤 " +
					"**Confirm**(확인)을 클릭하세요:"),
				snippet("mcpservers.json"),
			},
			Docs: "https://docs.trae.ai/ide/model-context-protocol",
		},
		"cline": {
			Summary: "Cline",
			Blocks: []block{
				para("**MCP Servers → Remote Servers**에서 이름에 `penguin-stats`, URL에 서버 URL을 입력하고 " +
					"Transport Type에서 **Streamable HTTP**를 선택한 뒤 **Add Server**를 클릭하세요. " +
					"`cline_mcp_settings.json`에서는 type을 반드시 명시하세요(기본값이 SSE입니다):"),
				snippet("streamable.json"),
				para("CLI: `{cline.sh}`"),
			},
			Docs: "https://docs.cline.bot/mcp/mcp-overview",
		},
	},
	Other: otherApps{
		Summary: "기타 앱",
		Head:    [3]string{"앱", "위치", "추가할 내용"},
		Rows: [][3]string{
			{"GitHub Copilot CLI", "터미널", "`{copilot.sh}`"},
			{"Windsurf / Devin Desktop (Cascade 에이전트)", "`mcp_config.json`", "`{windsurf.json}` (`serverUrl` 또는 `url`)"},
			{"Devin CLI, Devin Desktop (Devin Local 에이전트)", "터미널", "`{devin.sh}`"},
			{"Zed", "`settings.json`", "`{zed.json}`"},
			{"JetBrains AI Assistant", "Settings → Tools → AI Assistant → Model Context Protocol (MCP) → Add → HTTP",
				"`{mcpservers.json}`"},
			{"LM Studio 0.3.17+", "[LM Studio에 추가]({lmstudio.link}) 또는 Program → Install → Edit mcp.json",
				"`{mcpservers.json}`"},
			{"opencode", "`opencode.json`", "`{opencode.json}`"},
			{"Kiro", "`~/.kiro/settings/mcp.json`", "`{mcpservers.json}`"},
			{"Goose", "[Goose에 추가]({goose.link}) 또는 `goose configure` → Remote Extension (Streamable HTTP)", "서버 URL"},
			{"Warp", "Settings → Agents → MCP servers → + Add", "`{warp.json}`"},
			{"Raycast (Pro)", `"Install MCP Server" 명령 → Transport는 HTTP`, "서버 URL"},
			{"여러 앱에 한 번에 추가(Node.js 필요)", "터미널", "`{add-mcp.sh}`"},
			{"그 밖의 앱", "", "원격 MCP 서버, Streamable HTTP, 서버 URL, 인증 없음"},
			{"로컬(stdio) 서버만 지원하는 앱", "설정 파일", "`{mcp-remote.json}` (Node.js 필요)"},
		},
	},
	Docs: "문서: ",

	Try: section{ID: "try", Heading: "사용해 보기", Blocks: []block{
		para("한국어, 영어, 중국어, 일본어 중 어느 언어로 물어봐도 됩니다. 아이템 이름은 네 언어 모두 인식하며, " +
			"중국어·일본어 별명과 중국어 병음도 인식합니다."),
		items(
			"KR 서버에서 원암 큐브는 어디서 파밍하는 게 좋을까요? 아이템당 필요 이성이 가장 낮은 스테이지 3개를 "+
				"알려 주세요.",
			"KR 서버 1-7에서는 뭐가 드랍되나요? 드랍률도 알려 주세요.",
			"지금 KR 서버에서 열려 있는 이벤트는 뭐가 있고, 이벤트 스테이지에서는 각각 뭐가 드랍되나요?",
			"KR 서버와 CN 서버에서 장치 파밍에 가장 좋은 스테이지를 비교해 주세요.",
			"JP 서버 데이터는 마지막으로 언제 업데이트됐나요?",
			"KR 서버에서 원암 큐브 드랍률이 가장 높은 스테이지 3개를 출력하는 Python 스크립트를 작성해 주세요. "+
				"데이터 기준 시각과 출처 표기도 함께 출력해 주세요.",
		),
		para(`**작동 확인:** "Penguin Statistics 데이터로 KR 서버에서 원암 큐브를 가장 적은 이성으로 파밍할 수 ` +
			`있는 곳을 알려 주세요"라고 물어보세요. 제대로 동작하면 답변에 서버와 데이터 기준 시각이 나오고, ` +
			`출처로 Penguin Statistics가 표기됩니다. 어시스턴트가 도구를 호출하지 않고 답하면 "Penguin Statistics 도구를 사용해 주세요"라고 말하세요.`),
	}},
	Plugin: section{ID: "plugin", Heading: "플러그인과 스킬(선택)", Blocks: []block{
		para("MCP 서버는 어시스턴트에게 데이터를 제공하고, `penguin-stats-api` 스킬은 그 데이터를 제대로 쓰는 법을 " +
			"알려 줍니다. 어느 게임 서버의 데이터인지 밝히고, 표본 수가 많은 데이터를 우선하며, 데이터 기준 시각과 " +
			"출처를 표기하고, REST API를 올바르게 호출하는 코드를 작성하도록 합니다. 플러그인을 설치하면 둘 다 " +
			"추가됩니다."),
		items(
			"Claude Code: `/plugin marketplace add penguin-statistics/agent-skills`, 그다음 "+
				"`/plugin install penguin-stats@penguin-statistics`",
			"Claude (웹, 데스크톱): **사용자 정의**(Customize)에서 **Plugins → Add → Add marketplace**"+
				"(마켓플레이스 추가)를 클릭해 `penguin-statistics/agent-skills`를 입력하고, **Penguin Statistics**를 "+
				"선택해 **Add**를 클릭한 다음, "+
				"플러그인의 **Connectors** 탭에서 서버를 추가하고 연결하세요(로그인 불필요, Team 및 Enterprise "+
				"플랜에서는 Owner가 커넥터를 추가)",
			"Codex: `codex plugin marketplace add penguin-statistics/agent-skills`, 그다음 "+
				"`codex plugin add penguin-stats@penguin-statistics`",
			"Gemini CLI: `gemini extensions install https://github.com/penguin-statistics/agent-skills`",
			"VS Code: `chat.plugins.enabled`를 켠 상태에서 **Chat: Install Plugin From Source** → "+
				"`https://github.com/penguin-statistics/agent-skills`",
			"스킬만 설치(모든 에이전트 지원): `npx skills add penguin-statistics/agent-skills`",
		),
		para("이전에 서버를 직접 추가했다면, Claude와 Claude Code에서는 그대로 두세요. 두 앱은 직접 추가한 서버와 " +
			"플러그인의 서버를 같은 서버로 취급합니다. 다른 앱에서는 직접 추가한 항목을 삭제하세요. 그러지 않으면 도구가 두 번 로드될 수 " +
			"있습니다. 소스: https://github.com/penguin-statistics/agent-skills"),
	}},

	ToolsHeading: "도구",
	ToolsIntro:   "모든 도구는 읽기 전용입니다. 아무것도 변경할 수 없으며 드랍 보고도 제출할 수 없습니다.",
	ToolsHead:    [2]string{"도구", "기능"},
	Tools: []tool{
		{"search_items", "중국어, 영어, 일본어, 한국어 이름, 중국어·일본어 별명(狗粮, 石头), 병음, ID로 아이템을 찾습니다"},
		{"search_stages", "스테이지 코드(1-7, S2-12), 스테이지 ID, 챕터나 이벤트 이름으로 스테이지를 찾습니다"},
		{"get_item_drop_rates", "지정한 서버에서 아이템을 파밍하기 좋은 스테이지를 보여 줍니다(표본 수, 드랍률, 아이템당 필요 이성)"},
		{"get_stage_drops", "스테이지에서 드랍되는 아이템과 각 아이템의 드랍률을 보여 줍니다"},
		{"list_events", "서버에서 개방 중이거나 개방 예정인 이벤트와 해당 스테이지를 보여 줍니다"},
		{"get_dataset_info", "수치의 의미, 서버별 데이터 갱신 시점, 주의 사항, 라이센스와 출처 표기 방법을 알려 줍니다"},
	},

	Data: section{ID: "data", Heading: "데이터와 라이센스", Blocks: []block{items(
		"플레이어가 보고한 드랍을 서버별로 집계합니다. 집계 대상은 CN 서버, US 서버(글로벌 영어 서버), JP 서버, "+
			"KR 서버입니다. 모든 답변에는 데이터가 계산된 시각이 포함됩니다.",
		"드랍률 = 드랍 수 ÷ 표본 수이며, 100%를 넘을 수 있습니다. 표본 수가 100회 미만이면 드랍률의 오차가 큽니다.",
		"라이센스: [CC BY-NC 4.0](https://creativecommons.org/licenses/by-nc/4.0/deed.ko). 출처를 표기하면 "+
			"비상업적 용도로 사용할 수 있습니다. 출처 표기: "+
			"`데이터: 펭귄 물류 데이터 분석 부서(penguin-stats.io), CC BY-NC 4.0` "+
			"(영어: `Data: Penguin Statistics (penguin-stats.io), CC BY-NC 4.0`). 서버는 어시스턴트에게 이 "+
			"출처를 답변에 넣도록 요청합니다.",
	)}},
	Limits: section{ID: "limits", Heading: "사용량 제한", Blocks: []block{items(
		"IP 주소마다 {window}당 {limit}회까지 요청할 수 있습니다(IPv6는 /64 단위). 질문 하나에 보통 2~4회의 "+
			"요청이 사용됩니다.",
		`제한을 넘으면 서버가 "rate limited, retry after N s"라고 응답합니다. 그 시간만큼 기다린 뒤 다시 시도하세요.`,
		"Claude.ai와 ChatGPT는 각 회사의 서버에서 접속하므로 이 앱들의 사용자가 한도를 나눠 쓰게 될 수 "+
			"있습니다. 이런 앱에서 이 메시지가 보이면 1분 정도 기다려 주세요.",
	)}},
	Privacy: section{ID: "privacy", Heading: "개인정보 보호", Blocks: []block{items(
		"계정도 API 키도 필요 없고, 쿠키도 사용하지 않습니다. 이 페이지는 다른 사이트의 스크립트를 불러오지 않으며 "+
			"분석 도구도 사용하지 않습니다.",
		"서버는 어시스턴트가 보내는 내용, 즉 도구 이름과 그 인수(예: 아이템 이름)만 받습니다. 대화 내용은 "+
			"서버가 볼 수 없습니다.",
		"요청마다 시각, IP 주소(및 해당 국가, 도시, 네트워크), User-Agent, MCP 클라이언트의 이름과 버전, "+
			"메서드와 도구 이름, 결과와 처리 시간을 기록합니다. 도구 인수는 기록하지 않지만, 호출이 실패하면 "+
			"기록되는 오류 메시지에 잘못된 인수(예: 찾을 수 없는 아이템 이름)가 포함될 수 있습니다.",
		"로그는 모니터링 서비스 제공업체(Datadog)에 15일 동안 보관된 뒤 삭제됩니다. 로그는 서비스 운영, 악용 "+
			"방지, 어떤 도구와 클라이언트가 쓰이는지 집계하는 데에만 사용합니다. 어떤 정보도 판매하거나 광고 "+
			"목적으로 공유하지 않습니다.",
		"요청은 Cloudflare를 거치며, Cloudflare가 자체 약관에 따라 처리합니다.",
	)}},
	Troubleshooting: section{ID: "troubleshooting", Heading: "문제 해결", Blocks: []block{
		items(
			"**연결되지 않거나 SSE 오류 또는 405 오류가 나는 경우:** 앱이 이 서버가 지원하지 않는 예전 SSE 전송 방식을 "+
				"사용하고 있을 수 있습니다. Streamable HTTP를 선택하세요. Cherry Studio와 Cline에서는 "+
				"`\"type\": \"streamableHttp\"`, Gemini CLI에서는 `httpUrl`을 사용하세요. 이 앱들에서는 `url`만 "+
				"쓰면 SSE로 취급됩니다.",
			"**VS Code에 서버가 나타나지 않는 경우:** 최상위 키는 `~/.copilot/mcp-config.json`과 워크스페이스의 "+
				"`.mcp.json`에서는 `mcpServers`, `.vscode/mcp.json`과 사용자 프로필의 `mcp.json`에서는 "+
				"`servers`입니다.",
			`**앱이 로그인을 요구하는 경우:** 로그인은 필요 없습니다. "로그인 없음"(No sign in) 또는 `+
				`"No authentication"(인증 없음)을 선택하세요.`,
			"**브라우저에서 `/mcp`를 열면 이 페이지가 나오는 경우:** 정상입니다. 이 엔드포인트는 AI 앱의 POST "+
				"요청만 받습니다.",
			`**"rate limited" 메시지가 나오는 경우:** '사용량 제한'을 참고하세요.`,
			"**도구를 추가했는데 어시스턴트가 쓰지 않는 경우:** 채팅에서 커넥터를 켜고(Claude: + → 커넥터, ChatGPT: @ 입력, "+
				`Cherry Studio: 에이전트에서 MCP 활성화) "Penguin Statistics 도구를 사용해 주세요"라고 말하세요.`,
			"**웹사이트와 수치가 다른 경우:** 서버(웹사이트는 다른 서버를 고르지 않으면 CN 서버를 보여 주고, "+
				"어시스턴트는 서버를 묻거나 사용 언어로 추측합니다)와 기간을 확인하고, 어시스턴트에게 어떤 서버와 "+
				"어느 시점의 데이터를 사용했는지 물어보세요.",
			"**중국 본토에서 접속하는 경우:** 아직 이 서버의 .cn 미러가 없습니다. 로컬 앱은 느리거나 연결되지 "+
				"않을 수 있습니다. Claude와 ChatGPT는 각자의 서버에서 접속하므로 영향을 받지 않습니다.",
		),
		para("**AI 없이 테스트하기:** `npx @modelcontextprotocol/inspector`를 실행하고 Streamable HTTP를 선택한 뒤 " +
			"URL을 입력하고 Connect, List Tools를 차례로 클릭하세요. 또는:"),
		snippet("curl.sh"),
	}},
	Links: section{ID: "links", Heading: "링크", Blocks: []block{items(
		"웹사이트: {site}",
		"REST API 문서: https://developer.penguin-stats.io/public-api · OpenAPI: {site}/swagger/doc.json",
		"이 안내의 Markdown 버전: {md} · 에이전트용: {llms} · 사이트 안내: {site}/llms.txt",
		"서버 카드: {card}",
		"소스 코드: {repo} (internal/controller/mcp)",
		"문제 제보: {repo}/issues",
	)}},
	Minute: "분",

	Footer: "설정 절차는 2026-10-23에 각 앱의 문서와 대조해 마지막으로 확인했습니다. 앱 메뉴는 자주 바뀝니다. " +
		"절차가 실제와 다르면 [이슈를 등록해 주세요]({repo}/issues).",
}
