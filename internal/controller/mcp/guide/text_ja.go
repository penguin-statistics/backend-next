package guide

// ja is the guide in Japanese. The labels of the apps stay in English, in bold, as Japanese readers read them; a few
// have a gloss: those of Claude's connector steps are the labels of its Japanese help article
// (https://support.claude.com/ja/articles/11175166, which names them in Japanese, while its Japanese article on
// plugins keeps Customize, Plugins and Add marketplace in English), the others (認証なし) are ours, as the Japanese
// interfaces of the apps were not checked. The apps are in the English order (Cherry Studio is mostly used in China).
// The names of the site and of the servers are those of the website (seo/texts.go), the items those of the API.
var ja = &text{
	Lang:            "ja",
	Name:            "日本語",
	Abbr:            "日",
	ContentLanguage: "ja",
	Title:           "ペンギン急便データ統計処理部門 MCP サーバー",
	TitleWords:      []string{"ペンギン急便", "データ", "統計", "処理部門"},
	Description: "AI アシスタントでアークナイツのドロップ率を調べられる、無料・読み取り専用の MCP サーバー（アカウント・" +
		"API キー不要）。Claude、ChatGPT、Cursor、VS Code、Cherry Studio などへの追加方法を説明します。",
	MarkdownLink: "Markdown",
	Copy:         "コピー",
	Copied:       "コピーしました",
	SkipLink:     "本文へスキップ",
	PageNav:      "このページの内容",
	LangNav:      "言語",
	AppNav:       "アプリ",
	Terminal:     "ターミナル",
	ForAgents:    "AI エージェント向け：本ページの Markdown 版は {md} 、設定手順は {llms} にあります。",
	Lead: []string{
		"AI アシスタントでアークナイツのドロップ率を調べられます。ペンギン急便データ統計処理部門（penguin-stats.io）は" +
			"大陸版・英語版・日本版・韓国版の全ステージでプレイヤーが報告したドロップを集計しています。このサーバーを接続すると、" +
			"Claude、ChatGPT、Cursor、VS Code、Cherry Studio などの AI アプリが数値を調べてくれます。",
		"MCP（Model Context Protocol）は、AI アプリを外部のツールにつなぐ標準の仕組みです。",
	},
	Badges: "無料 · 登録不要 · API キー不要 · 読み取り専用",

	URLLabel: "サーバー URL",
	Card: [][2]string{
		{"トランスポート", "Streamable HTTP（SSE ではありません）"},
		{"認証", "なし。聞かれたら「No authentication」か「No sign in」（サインインなし）を選びます。"},
		{"推奨名", "`penguin-stats`"},
	},
	URLNote: "URL は末尾の `/mcp` まで表示どおりに入力してください。このページ（{guide} ）は案内で、サーバーではありません。",

	OneClick:     "ワンクリックで追加：",
	AddTo:        "%s に追加",
	OneClickApps: []string{"claude", "cursor", "vscode", "cherry-studio"},
	OneClickNote: "設定を入力済みの状態でアプリが開きます。確認してから確定してください。",

	AgentSummary: "エージェントに設定してもらう",
	AgentIntro:   "次のプロンプトを Claude Code、Codex、Cursor などのコーディングエージェントに貼り付けてください：",
	AgentPrompt: "このエージェントに Penguin Statistics の MCP サーバーを追加してください（URL {url} 、Streamable HTTP、" +
		"認証なし）。{llms} のこのクライアント用の手順でユーザー（グローバル）スコープに追加し、ツール一覧で動作を" +
		"確認してください。",
	AgentCopy: "エージェント用プロンプトをコピー",
	AgentHint: "Claude Code、Codex、Cursor などのコーディングエージェントに貼り付けます。",
	AgentMore: "すべて表示",
	AgentLess: "折りたたむ",

	Install:     "インストール",
	ChatApps:    "チャットアプリ",
	CodingTools: "コーディングツール",
	Chat:        []string{"claude", "chatgpt", "cherry-studio"},
	Coding:      []string{"claude-code", "cursor", "vscode", "codex", "gemini-cli", "trae", "cline"},
	Clients: map[string]clientText{
		"claude": {
			Summary: "Claude（claude.ai、Claude Desktop、モバイルアプリ）",
			Blocks: []block{
				steps(
					"[Claude に追加]({claude.link})を開くか、**Customize → Connectors → + Add → Add custom connector**"+
						"（カスタマイズ → コネクタ → + 追加 → カスタムコネクタを追加）と進みます。",
					"名前は `Penguin Statistics`、リモート MCP サーバー URL は `{url}` にして **Continue** をクリックします。",
					"**Authentication** で **No sign in**（サインインなし）を選び、**Add** をクリックします。",
					"チャットで **+ → Connectors** を開き、Penguin Statistics をオンにします。",
				),
				para("Free プランのカスタムコネクタは 1 つまでです。Team・Enterprise プランではオーナーが " +
					"**Organization settings → Connectors**（組織設定 → コネクタ）で追加し、メンバーが **Connect** をクリックします。" +
					"モバイルアプリでは追加できず、Web かデスクトップで追加すると同期されます。同じアカウントの Claude Code でも" +
					"使えます。"),
			},
			Docs: "https://support.claude.com/ja/articles/11175166",
		},
		"chatgpt": {
			Summary: "ChatGPT（Web 版）",
			Blocks: []block{
				steps(
					"https://chatgpt.com/plugins で **+** → **Add custom MCP server** を選びます。",
					"Name に `Penguin Statistics`、Server URL に `{url}` を入力します。",
					"Authentication で **No authentication**（認証なし）を選び、警告を読んで **I understand and want to continue**、"+
						"**Create as a plugin** の順にクリックします。",
					"プラグインをインストールし、チャットで **@** を入力して Penguin Statistics を選びます。",
				),
				para("利用可否はプランとワークスペースの設定次第です。ChatGPT デスクトップアプリは Codex の設定を使います" +
					"（下の Codex を参照）。"),
			},
			Docs: "https://developers.openai.com/api/docs/guides/custom-mcp-server",
		},
		"cherry-studio": {
			Summary: "Cherry Studio",
			Blocks: []block{
				para("ワンクリック：[Cherry Studio に追加]({cherry-studio.link})（新しいバージョンが必要）。サーバーはオフで追加される" +
					"ので、確認してからオンにします。反応がなければ手動で追加します。"),
				para("手動：**Settings → MCP → MCP Servers → Add → Import from JSON** で次の内容を貼り付けます："),
				snippet("streamable.json"),
				para("`\"type\": \"streamableHttp\"` は省略しないでください。省略すると古い SSE で接続しようとして失敗します。"),
				para("次に **Work → Agent → Edit → MCP** でエージェントに対してこのサーバーを有効にします（メニューはバージョンにより異なる" +
					"ことがあります）。"),
			},
			Docs: "https://docs.cherryai.com.cn/advanced-basic/extensions/mcp",
		},
		"claude-code": {
			Summary: "Claude Code",
			Blocks: []block{
				plugin(para("おすすめはプラグインで、サーバーとスキルをまとめて追加します（下の「プラグインとスキル」参照）：")),
				plugin(snippet("claude-plugin.sh")),
				plugin(para("サーバーだけなら：")),
				snippet("claude-code.sh"),
				para("`claude mcp list` か `/mcp` で確認できます。プロジェクトの `.mcp.json` では `\"type\": \"http\"` を省略しないで" +
					"ください："),
				snippet("claude-code.json"),
			},
			Docs: "https://code.claude.com/docs/ja/mcp",
		},
		"cursor": {
			Summary: "Cursor",
			Blocks: []block{
				para("[Cursor に追加]({cursor.link})を使うか、`~/.cursor/mcp.json` に追加します："),
				snippet("mcpservers.json"),
			},
			Docs: "https://cursor.com/docs/mcp",
		},
		"vscode": {
			Summary: "VS Code（GitHub Copilot）",
			Blocks: []block{
				para("[VS Code に追加]({vscode.link}) · [VS Code Insiders]({vscode-insiders.link})"),
				para("または、コマンドパレットで **MCP: Add Server** を実行し、**HTTP (HTTP or Server-Sent Events)** を選んで URL を" +
					"貼り付け、サーバー ID に `penguin-stats` と入力します。次に **Global**、聞かれたら **Copilot Global** を選びます。" +
					"保存先は `~/.copilot/mcp-config.json`（`COPILOT_HOME` 設定時は `$COPILOT_HOME/mcp-config.json`）で、手で編集も" +
					"できます。トップレベルのキーは `mcpServers` です："),
				snippet("copilot.json"),
				para("または次のコマンドで、ユーザープロファイルの `mcp.json` に追加します："),
				snippet("vscode.sh"),
				para("互換性のために残る `.vscode/mcp.json` とユーザープロファイルの `mcp.json` は VS Code 形式で、" +
					"トップレベルのキーは `mcpServers` ではなく `servers` です："),
				snippet("vscode.json"),
			},
			Docs: "https://code.visualstudio.com/docs/agent-customization/mcp-servers",
		},
		"codex": {
			Summary: "Codex（CLI、IDE 拡張機能、ChatGPT デスクトップアプリ）",
			Blocks: []block{
				snippet("codex.sh"),
				para("または `~/.codex/config.toml` に追加します："),
				snippet("codex.toml"),
				para("IDE 拡張機能と ChatGPT デスクトップアプリもこのファイルを使います。画面からは **Settings → MCP servers → " +
					"Add server → Streamable HTTP** で URL を貼り付け、**Save** して **Restart** します。"),
			},
			Docs: "https://learn.chatgpt.com/docs/extend/mcp",
		},
		"gemini-cli": {
			Summary: "Gemini CLI",
			Blocks: []block{
				snippet("gemini.sh"),
				para("または `~/.gemini/settings.json` で `httpUrl` を使います（Gemini の `url` は古い SSE を指します）："),
				snippet("gemini.json"),
			},
			Docs: "https://geminicli.com/docs/tools/mcp-server/",
		},
		"trae": {
			Summary: "Trae",
			Blocks: []block{
				para("**Settings → MCP → Add → Create Manually** で次の内容を貼り付け、**Confirm** をクリックします："),
				snippet("mcpservers.json"),
			},
			Docs: "https://docs.trae.ai/ide/model-context-protocol",
		},
		"cline": {
			Summary: "Cline",
			Blocks: []block{
				para("**MCP Servers → Remote Servers** で名前（`penguin-stats`）と URL を入力し、Transport Type を " +
					"**Streamable HTTP** にして **Add Server** をクリックします。`cline_mcp_settings.json` ではタイプを明示します" +
					"（デフォルトは SSE）："),
				snippet("streamable.json"),
				para("CLI：`{cline.sh}`"),
			},
			Docs: "https://docs.cline.bot/mcp/mcp-overview",
		},
	},
	Other: otherApps{
		Summary: "その他のアプリ",
		Head:    [3]string{"アプリ", "場所", "追加する内容"},
		Rows: [][3]string{
			{"GitHub Copilot CLI", "シェル", "`{copilot.sh}`"},
			{"Windsurf / Devin Desktop（Cascade エージェント）", "`mcp_config.json`", "`{windsurf.json}`（`serverUrl` か `url`）"},
			{"Devin CLI、Devin Desktop（Devin Local エージェント）", "シェル", "`{devin.sh}`"},
			{"Zed", "`settings.json`", "`{zed.json}`"},
			{"JetBrains AI Assistant", "Settings → Tools → AI Assistant → Model Context Protocol (MCP) → Add → HTTP",
				"`{mcpservers.json}`"},
			{"LM Studio 0.3.17+", "[LM Studio に追加]({lmstudio.link})、または Program → Install → Edit mcp.json",
				"`{mcpservers.json}`"},
			{"opencode", "`opencode.json`", "`{opencode.json}`"},
			{"Kiro", "`~/.kiro/settings/mcp.json`", "`{mcpservers.json}`"},
			{"Goose", "[Goose に追加]({goose.link})、または `goose configure` → Remote Extension (Streamable HTTP)", "URL"},
			{"Warp", "Settings → Agents → MCP servers → + Add", "`{warp.json}`"},
			{"Raycast（Pro）", "「Install MCP Server」コマンド → Transport は HTTP", "URL"},
			{"複数のアプリに一括追加（Node.js）", "シェル", "`{add-mcp.sh}`"},
			{"上記以外", "", "リモート MCP サーバー、Streamable HTTP、URL、認証なし"},
			{"ローカル（stdio）サーバーのみ対応のアプリ", "設定ファイル", "`{mcp-remote.json}`（Node.js が必要）"},
		},
	},
	Docs: "ドキュメント：",

	Try: section{ID: "try", Heading: "使ってみる", Blocks: []block{
		para("日本語・英語・中国語・韓国語のどれでも質問でき、正式名のほか中国語の通称やピンインも使えます。"),
		items(
			"日本版の初級源岩の周回場所を、1 個あたりの必要理性が少ない順に 3 つ教えてください。",
			"日本版の 1-7 のドロップとドロップ率を教えてください。",
			"日本版で開催中のイベントと各ステージのドロップを教えてください。",
			"初級装置の最適な周回場所を日本版と大陸版で比較してください。",
			"英語版のデータの最終更新はいつですか？",
			"日本版で初級源岩のドロップ率上位 3 ステージを、データの日時とクレジット付きで表示する Python スクリプトを書いてください。",
		),
		para("**動作確認：**「Penguin Statistics で日本版の初級源岩の効率のいい周回場所を調べて」と聞いてください。" +
			"正しく動いていれば、サーバー、データの日時、出典を示して答えます。ツールを使わずに答えたら" +
			"「Penguin Statistics のツールを使って」と伝えてください。"),
	}},
	Plugin: section{ID: "plugin", Heading: "プラグインとスキル（任意）", Blocks: []block{
		para("MCP サーバーはアシスタントにデータを渡し、`penguin-stats-api` スキルはその使い方（サーバーの明示、" +
			"サンプル数の多いデータの優先、データの日時とクレジットの記載、REST API を正しく使うコード）を教えます。" +
			"プラグインで両方が入ります。"),
		items(
			"Claude Code：`/plugin marketplace add penguin-statistics/agent-skills` → "+
				"`/plugin install penguin-stats@penguin-statistics`",
			"Claude（Web、デスクトップ）：**Customize → Plugins → Add → Add marketplace** で "+
				"`penguin-statistics/agent-skills` を追加し、**Penguin Statistics** を選んで **Add** をクリックし、プラグインの "+
				"**Connectors** タブでサーバーを追加・接続します（サインイン不要。Team・Enterprise プランではオーナーがコネクタを追加）。",
			"Codex：`codex plugin marketplace add penguin-statistics/agent-skills` → "+
				"`codex plugin add penguin-stats@penguin-statistics`",
			"Gemini CLI：`gemini extensions install https://github.com/penguin-statistics/agent-skills`",
			"VS Code：`chat.plugins.enabled` をオンにして **Chat: Install Plugin From Source** → "+
				"`https://github.com/penguin-statistics/agent-skills`",
			"スキルのみ（どのエージェントでも）：`npx skills add penguin-statistics/agent-skills`",
		),
		para("手動でサーバーを追加済みの場合、Claude と Claude Code ではプラグインのものと同一扱いなので、そのままで構いません。" +
			"ほかのアプリでは、ツールが二重に読み込まれないよう手動の項目を削除してください。" +
			"ソースコード：https://github.com/penguin-statistics/agent-skills"),
	}},

	ToolsHeading: "ツール",
	ToolsIntro:   "すべて読み取り専用です。データの変更やドロップの報告はできません。",
	ToolsHead:    [2]string{"ツール", "できること"},
	Tools: []tool{
		{"search_items", "中・英・日・韓の名前、通称（狗粮、石头）、ピンイン、ID でアイテムを検索"},
		{"search_stages", "コード（1-7、S2-12）、ステージ ID、章・イベント名でステージを検索"},
		{"get_item_drop_rates", "サーバー別のアイテムの周回場所：サンプル数、ドロップ率、1 個あたりの必要理性"},
		{"get_stage_drops", "ステージのドロップとアイテムごとのドロップ率"},
		{"list_events", "サーバーで開催中・開催予定のイベントとそのステージ"},
		{"get_dataset_info", "数値の意味、各サーバーのデータの鮮度、注意点、ライセンスとクレジット"},
	},

	Data: section{ID: "data", Heading: "データとライセンス", Blocks: []block{items(
		"プレイヤーのドロップ報告を、大陸版（CN）、英語版（US、グローバル版）、日本版（JP）、韓国版（KR）のサーバー別に"+
			"集計しています。回答にはデータの計算日時が付きます。",
		"ドロップ率 = ドロップ数 ÷ サンプル数で、100% を超えることもあります。サンプル数 100 未満の値はばらつきが大きくなります。",
		"ライセンス：[CC BY-NC 4.0](https://creativecommons.org/licenses/by-nc/4.0/deed.ja)。クレジット表示のうえ非営利で"+
			"利用できます：`データ：ペンギン急便データ統計処理部門（penguin-stats.io）、CC BY-NC 4.0`"+
			"（英語：`Data: Penguin Statistics (penguin-stats.io), CC BY-NC 4.0`）。サーバーはアシスタントに出典の明記を求めています。",
	)}},
	Limits: section{ID: "limits", Heading: "利用制限", Blocks: []block{items(
		"上限は IP アドレスごとに {limit} リクエスト/{window}（IPv6 は /64 単位）です。1 回の質問で通常 2〜4 リクエストを使います。",
		"上限を超えると「rate limited, retry after N s」が返ります。その秒数待ってから再試行してください。",
		"Claude.ai と ChatGPT は各社のサーバーから接続するので、ユーザー間で上限を共有することがあります。"+
			"これらのアプリでこのメッセージが出たら 1 分ほど待ってください。",
	)}},
	Privacy: section{ID: "privacy", Heading: "プライバシー", Blocks: []block{items(
		"アカウントもキーも Cookie も使いません。このページは外部のスクリプトを読み込まず、アクセス解析もありません。",
		"サーバーが受け取るのは、アシスタントが送るツール名と引数（アイテム名など）だけで、会話は見えません。",
		"リクエストごとに日時、IP アドレス（とその国・都市・ネットワーク）、User-Agent、MCP クライアント名とバージョン、"+
			"メソッドとツール名、結果、処理時間を記録します。ツールの引数は記録しませんが、失敗時に記録するエラーメッセージに"+
			"誤った引数（存在しないアイテム名など）が含まれることがあります。",
		"ログは監視サービス（Datadog）で 15 日間保存後に削除します。用途は運用、不正利用の防止、ツールとクライアントの"+
			"利用集計のみで、販売や広告目的の共有はしません。",
		"リクエストは Cloudflare を経由し、同社の規約に従って処理されます。",
	)}},
	Troubleshooting: section{ID: "troubleshooting", Heading: "困ったときは", Blocks: []block{
		items(
			"**「接続できない」、SSE のエラー、405：** 非対応の古い SSE トランスポートを使っている可能性があります。"+
				"Streamable HTTP を選んでください（Cherry Studio と Cline は `\"type\": \"streamableHttp\"`、Gemini CLI は "+
				"`httpUrl`）。これらのアプリでは `url` だけだと SSE になります。",
			"**VS Code にサーバーが出ない：** トップレベルのキーは、`~/.copilot/mcp-config.json` とワークスペースの `.mcp.json` では "+
				"`mcpServers`、`.vscode/mcp.json` とユーザープロファイルの `mcp.json` では `servers` です。",
			"**サインインを求められる：** 不要です。「No sign in」（サインインなし）か「No authentication」を選びます。",
			"**ブラウザで `/mcp` を開くとこのページが表示される：** 正常です。このエンドポイントは AI アプリからの POST だけを受け付けます。",
			"**「rate limited」と出る：** 「利用制限」を参照してください。",
			"**追加したツールが使われない：** チャットでコネクタをオンにし（Claude：+ → Connectors、ChatGPT：@ を入力、"+
				"Cherry Studio：エージェントで MCP を有効化）、「Penguin Statistics を使って」と頼みます。",
			"**数値がサイトと違う：** サーバー（サイトの既定は大陸版、アシスタントは尋ねるか言語から推測します）と期間を確認し、"+
				"使ったサーバーとデータの日時をアシスタントに聞いてください。",
			"**中国本土から：** まだ .cn ミラーがなく、ローカルのアプリは遅いか接続できないことがあります。"+
				"Claude と ChatGPT は自社のサーバーから接続するので影響を受けません。",
		),
		para("**AI なしでテスト：** `npx @modelcontextprotocol/inspector` を実行し、Streamable HTTP を選んで URL を入力し、" +
			"Connect、List Tools の順にクリックします。または："),
		snippet("curl.sh"),
	}},
	Links: section{ID: "links", Heading: "リンク", Blocks: []block{items(
		"ウェブサイト：{site}",
		"REST API ドキュメント：https://developer.penguin-stats.io/public-api · OpenAPI：{site}/swagger/doc.json",
		"このガイドの Markdown 版：{md} · エージェント向け：{llms} · サイトのガイド：{site}/llms.txt",
		"サーバーカード：{card}",
		"ソースコード：{repo} （internal/controller/mcp）",
		"問題の報告：{repo}/issues",
	)}},
	Minute: "分",

	Footer: "手順は 2026-10-23 に各アプリのドキュメントで確認しました。メニューはよく変わるので、古くなっていたら " +
		"[issue を作成]({repo}/issues)してください。",
}
