package guide

// text is the guide in one language: every word of it. Commands, configuration and one-click links are the same in
// every language, made of the URL of the server: they are in clients.go, and the texts refer to them by placeholder.
//
// Texts are written in a small part of Markdown, the same for both renderings: `code`, **bold**, [text](URL), and
// bare https:// URLs, which become links (a URL ends at a space or a character outside ASCII). They hold placeholders
// in braces, which Render expands: {url} (the server), {guide} (this guide), {md} (its Markdown in the language of the
// text), {llms} (llms.txt), {card} (the server card), {site} (penguin-stats.io), {repo} (this repository), {limit} and
// {window} (the rate limit), {tools} (the names of the tools) and {toolcount}, plus the snippets and links of
// clients.go ({cursor.json}, {claude.link}).
type text struct {
	// Lang is the value of ?lang for the text; ContentLanguage tells its language in Content-Language, <html lang>
	// and hreflang
	Lang, ContentLanguage string
	// Name is the name of the language in itself, in the language switch; Abbr is its abbreviation there on phones
	Name, Abbr string
	// Title is the heading of the page; Description sums it up for search results and link previews
	Title, Description string
	// TitleWords are the words of the name of the site in Title, for a language written without spaces (Japanese): the
	// heading breaks between them only, not inside a word
	TitleWords []string
	// MarkdownLink names the link to the Markdown of the page
	MarkdownLink string
	// Copy and Copied label the copy buttons, before and after a click
	Copy, Copied string
	// SkipLink skips to the content; PageNav, LangNav and AppNav name the links to the sections, the language switch
	// and the list of apps for screen readers; Terminal labels the blocks of shell commands. The Markdown has none.
	SkipLink, PageNav, LangNav, AppNav, Terminal string
	// ForAgents points agents reading the HTML to the Markdown and llms.txt; screens do not show it. It is plain text,
	// with no markup: a link in it would take the focus of keyboard users, who would not see where it went.
	ForAgents string
	// Lead says what the server is, and what MCP is, for players who are not developers
	Lead   []string
	Badges string

	// the card of the connection: the URL of the server (with a copy button), then the other rows
	URLLabel string
	Card     [][2]string
	// URLNote warns that the URL ends in /mcp, and that this page is not the server
	URLNote string

	// OneClick introduces the one-click links of the apps of OneClickApps, each labelled AddTo with the name of
	// the app; OneClickNote says they only fill the app in
	OneClick     string
	AddTo        string
	OneClickApps []string
	OneClickNote string

	// a prompt to paste into a coding agent, which then adds the server itself: the first way to install it. The
	// Markdown has AgentSummary as its heading and AgentIntro before it. The page leads with a button that copies it
	// (AgentCopy), AgentHint under it, and the prompt itself, which phones show in part until AgentMore unfolds it
	// (and AgentLess folds it again); AgentSummary names the group of them for screen readers.
	AgentSummary, AgentIntro, AgentPrompt      string
	AgentCopy, AgentHint, AgentMore, AgentLess string

	// Install has the sections of the apps: ChatApps and CodingTools in their order, by the id of their section
	// in clients (which is their anchor, /#cursor), then the table of the others
	Install, ChatApps, CodingTools string
	Chat, Coding                   []string
	Clients                        map[string]clientText
	Other                          otherApps
	// Docs comes before the link to the documentation of an app
	Docs string

	Try    section
	Plugin section

	ToolsHeading, ToolsIntro string
	ToolsHead                [2]string
	Tools                    []tool

	Data, Limits, Privacy, Troubleshooting, Links section
	// Minute is the window of the rate limit when it is a minute ({window})
	Minute string

	// Footer says when the steps were last checked against the documentation of the apps
	Footer string

	// LLMsPreface comes first in llms.txt, which is the English Markdown for agents
	LLMsPreface []string
}

// clientText tells how to add the server to an app: the title of its section, what to do, and the documentation
type clientText struct {
	Summary string
	Blocks  []block
	Docs    string
}

// otherApps is the table of the apps without a section of their own
type otherApps struct {
	Summary string
	Head    [3]string
	Rows    [][3]string
}

// section is a part of the page, with its anchor
type section struct {
	ID, Heading string
	Blocks      []block
}

// block is a paragraph (Text), a list (Items), a numbered list (Steps) or a block of code (Code, in the language
// Lang for Markdown). A block of Plugin is shown only while the plugin is available (see pluginsAvailable).
type block struct {
	Text   string
	Items  []string
	Steps  []string
	Code   string
	Lang   string
	Plugin bool
}

// tool is a line of the table of the tools
type tool struct {
	Name, Line string
}

func para(s string) block { return block{Text: s} }

func items(s ...string) block { return block{Items: s} }

func steps(s ...string) block { return block{Steps: s} }

// snippet is the block of code of a snippet of clients.go, by its name
func snippet(name string) block { return block{Code: "{" + name + "}", Lang: langOf(name)} }

// plugin marks b as about the plugin
func plugin(b block) block {
	b.Plugin = true
	return b
}
