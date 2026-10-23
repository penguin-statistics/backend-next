package guide

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	htmltemplate "html/template"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The HTML page shows the texts as people read them, which the Markdown, for agents, does not need: blocks of code
// with a label, highlighted, the JSON indented; the lists of a section in the form that suits them (prompts to copy,
// rows of a label and a value, questions and answers); lines that do not break inside a name, a flag or a host
// (penguin-|stats), nor between a scheme and its host (https://|mcp.), nor start with a lone punctuation mark; and
// the words of other languages in English marked as such (中文, 日本語, 한국어), for screen readers and the glyphs of
// their fonts. Everything here is made of the texts at startup.

// blockView is a block of the texts as the page shows it.
type blockView struct {
	// Kind is p, buttons, steps, list, prompts, rows, faq or code
	Kind string
	// Class is that of a paragraph (callout) or of a list (dash)
	Class string
	// HTML is the text of a paragraph
	HTML htmltemplate.HTML
	// Items are the items of a list, Links the links of a row of buttons
	Items []itemView
	Links []linkView
	Code  codeView
}

// itemView is an item of a list.
type itemView struct {
	// Label is the label of a row or the question of a faq, HTML the rest
	Label, HTML htmltemplate.HTML
	// Copy is the text a prompt copies
	Copy string
	// Snips are the commands a row is made of, if it is only commands
	Snips []snipView
}

// snipView is a command to copy, after Join (then)
type snipView struct {
	Join string
	HTML htmltemplate.HTML
	Copy string
}

type linkView struct {
	URL   htmltemplate.URL
	Label htmltemplate.HTML
}

// codeView is a block of code: its label (the file it goes in, Terminal, JSON), the text to copy, and its lines
type codeView struct {
	Kind, Label, Copy string
	Lines             []lineView
}

type lineView struct {
	// Indent is the depth of the line, which a wrapped line hangs at
	Indent int
	HTML   htmltemplate.HTML
	// Break ends the line with a newline: the lines are blocks on the page, and the newline, which the style hides,
	// is for what reads the page without it (textContent, the tools turning HTML into Markdown or text)
	Break bool
}

// panelView is the section of an app in the install window.
type panelView struct {
	ID, Name, Qualifier string
	Blocks              []blockView
	DocsLabel, DocsText string
	DocsURL             htmltemplate.URL
}

// otherView is a row of the table of other apps: the app, where the server goes and what to add, a command or a
// configuration to copy (Snip) with a note, or a text.
type otherView struct {
	App, Where, What htmltemplate.HTML
	Snip             *snipView
}

// listKinds are the forms of the lists of the sections that have one
var listKinds = map[string]string{"try": "prompts", "plugin": "rows", "troubleshooting": "faq", "links": "rows"}

// typesetter typesets the texts of a language.
type typesetter struct {
	zh     bool
	expand func(string) string
	// terminal labels the blocks of shell commands
	terminal string
}

// text renders s, a text, as HTML, typeset.
func (ts typesetter) text(s string) (htmltemplate.HTML, error) {
	h, err := inline(ts.expand(s))
	if err != nil {
		return "", err
	}
	return htmltemplate.HTML(ts.typeset(string(h))), nil //nolint:gosec // inline escapes, typeset adds tags only
}

var (
	// a code with the punctuation around it, which a line must not separate from it
	gluedCode = regexp.MustCompile(`([(\[“"'「（]+)?(<code>[^<]*</code>)([)\]”"'.,;:!?」）。，、；：]+)?`)
	// a word of a name with hyphens, which a line must not break at them: penguin-stats.io, S2-12, 1-7
	hyphenated = regexp.MustCompile(`[(\[“"「（]?[A-Za-z0-9][A-Za-z0-9.:/@_]*-[A-Za-z0-9.:/@_-]*[A-Za-z0-9/][)\]”"」）.,;:]?`)
	// a name joined by hyphens, or a flag: penguin-stats, agent-skills, mcp-remote, --transport, -y
	hyphenWord = regexp.MustCompile(`-{1,2}[A-Za-z0-9_]+(?:-[A-Za-z0-9_]+)*|[A-Za-z0-9_]+(?:-[A-Za-z0-9_]+)+`)
	// a space before Chinese punctuation, which follows a URL in the Chinese texts so that the Markdown does not take
	// the punctuation into the URL
	spacedPunct = regexp.MustCompile(` ([，。）、；：])`)
	// a word of Chinese, Japanese or Korean in an English text: 中文, 日本語, 한국어, 狗粮
	cjkWord   = regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]+`)
	tagOrText = regexp.MustCompile(`<[^>]*>|[^<]+`)
	tag       = regexp.MustCompile(`<[^>]*>`)
	// a long code: its URLs, and the rest
	urlOrText = regexp.MustCompile(`https://[^\s"'<]+|[^h]+|h`)
	// a code first, then a note
	codeFirst = regexp.MustCompile("^`([^`]+)`(.*)$")
	// a bare URL in escaped text
	bareURL = regexp.MustCompile(`https://[^\s<"]+[^\s<".,;:!?)]`)
)

// shortCode is the longest code that does not break (wider than 360 px; narrower, it may break as a long one does)
const shortCode = 36

// opening is the punctuation that opens, which a line does not end with
const opening = "([“「（"

// shortWord is the longest name with hyphens that never breaks: a longer one may break at its hyphens, at 360 px
// and narrower
const shortWord = 24

// typeset typesets h, HTML from inline: codes and links are kept on a line with the punctuation around them; short
// codes do not break, long ones only at a slash, a comma or a brace; URLs break after a slash of their path; names
// never break at a hyphen; in English, the words of other languages are marked with their language; and in Chinese,
// no line starts with punctuation, there is no space before it, and the last line of a paragraph is not a lone
// character.
func (ts typesetter) typeset(h string) string {
	h = gluedCode.ReplaceAllStringFunc(h, func(m string) string {
		if strings.HasPrefix(m, "<code>") && strings.HasSuffix(m, "</code>") {
			return m
		}
		return `<span class="nb">` + m + "</span>"
	})
	var b strings.Builder
	in, glued := "", false
	for _, seg := range tagOrText.FindAllString(h, -1) {
		switch {
		case seg == "<code>":
			in = "code"
			b.WriteString(seg)
			continue
		case strings.HasPrefix(seg, "<a "):
			in = "a"
			// the bracket before a link stays with it: (https://…
			s := b.String()
			if r, size := utf8.DecodeLastRuneInString(s); strings.ContainsRune(opening, r) {
				b.Reset()
				b.WriteString(s[:len(s)-size] + `<span class="nw">` + string(r))
				glued = true
			}
		case seg == "</a>" && glued:
			in, glued = "", false
			b.WriteString(seg + "</span>")
			continue
		case seg == "</code>", seg == "</a>":
			in = ""
		}
		if strings.HasPrefix(seg, "<") {
			b.WriteString(seg)
			continue
		}
		switch in {
		case "code":
			// the tag is written: mark it as short, or break the code
			switch n := utf8.RuneCountInString(html.UnescapeString(seg)); {
			case strings.HasPrefix(seg, "https://"):
				b.WriteString(breakURL(seg))
			case n <= shortCode:
				s := b.String()
				b.Reset()
				b.WriteString(strings.TrimSuffix(s, "<code>") + `<code class="nw">` + breakShort(seg))
			default:
				b.WriteString(breakLong(seg))
			}
		case "a":
			if strings.HasPrefix(seg, "https://") {
				seg = breakURL(seg)
			}
			b.WriteString(seg)
		default:
			seg = hyphenated.ReplaceAllStringFunc(seg, func(w string) string {
				if len(w) > shortWord {
					return `<span class="nb">` + w + "</span>"
				}
				return `<span class="nw">` + w + "</span>"
			})
			if ts.zh {
				// full-width punctuation has its own space, and no line starts with it (line-break: strict)
				seg = spacedPunct.ReplaceAllString(seg, "$1")
			} else {
				seg = cjkWord.ReplaceAllStringFunc(seg, func(w string) string {
					return `<span lang="` + wordLang(w) + `">` + w + "</span>"
				})
			}
			b.WriteString(seg)
		}
	}
	h = b.String()
	if ts.zh {
		h = bindLastLine(h)
	}
	return h
}

// breakURL lets the URL u (escaped) break after the slashes of its path and before its query, and never at a hyphen.
// Its scheme and host, with the slash after it, are a box (u) that moves to the next line whole, and breaks after
// the scheme only if it is wider than the line: https://|mcp.penguin-stats.io/ in the steps of an app on a phone
// narrower than 355 px.
func breakURL(u string) string {
	return breakURLAfter("", u)
}

// breakURLAfter is breakURL with open, the quote or the bracket before the URL, in the box of its host: "https://…
func breakURLAfter(open, u string) string {
	rest, ok := strings.CutPrefix(u, "https://")
	if !ok {
		return u
	}
	host, path, slash := strings.Cut(rest, "/")
	if slash {
		host += "/"
	}
	// after each slash that something follows, and before a query
	var b strings.Builder
	if slash && path != "" {
		b.WriteString("<wbr>")
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c == '?' {
			b.WriteString("<wbr>")
		}
		b.WriteByte(c)
		if c == '/' && i+1 < len(path) {
			b.WriteString("<wbr>")
		}
	}
	return `<span class="u">` + open + "https://<wbr>" + nowrapWords(host) + "</span>" + nowrapWords(b.String())
}

// openers are the quotes and the brackets that a code may open before a URL
var openers = []string{`"`, "'", "(", "["}

// breakShort lets a short code (escaped) break where a long one does, but only where short codes break at all (at
// 360 px and narrower, which releases code.nw): a <wbr> breaks even in white-space: nowrap, a space of zero width
// (wb, in the style, so that it is not copied) does not.
func breakShort(s string) string {
	return strings.ReplaceAll(breakLong(s), "<wbr>", `<span class="wb"></span>`)
}

// breakLong lets a long code (escaped) break after a slash, a comma or an opening brace, and never at a hyphen.
func breakLong(s string) string {
	var b strings.Builder
	toks := urlOrText.FindAllString(s, -1)
	open := ""
	for i, tok := range toks {
		if strings.HasPrefix(tok, "https://") {
			b.WriteString(breakURLAfter(open, tok))
			open = ""
			continue
		}
		// the quote or the bracket before a URL goes with it
		if i+1 < len(toks) && strings.HasPrefix(toks[i+1], "https://") {
			for _, o := range openers {
				if t, ok := strings.CutSuffix(tok, o); ok {
					tok, open = t, o
					break
				}
			}
		}
		var t strings.Builder
		for i := 0; i < len(tok); i++ {
			t.WriteByte(tok[i])
			if strings.IndexByte("/,{", tok[i]) >= 0 && i > 0 && i+1 < len(tok) && tok[i+1] != '/' {
				t.WriteString("<wbr>")
			}
		}
		b.WriteString(nowrapWords(t.String()))
	}
	return b.String()
}

// lastLine is how many characters of a Chinese paragraph its last line keeps at least
const lastLine = 4

// bindLastLine keeps the last characters of h, Chinese text, on one line, should the paragraph end in Chinese.
func bindLastLine(h string) string {
	if strings.HasSuffix(h, ">") {
		return h
	}
	i, n, han := len(h), 0, false
	for i > 0 && n < lastLine {
		r, size := utf8.DecodeLastRuneInString(h[:i])
		if !unicode.Is(unicode.Han, r) && !isCJKPunct(r) {
			break
		}
		han = han || unicode.Is(unicode.Han, r)
		i -= size
		n++
	}
	if n < 2 || !han {
		return h
	}
	return h[:i] + `<span class="nw">` + h[i:] + "</span>"
}

func isCJKPunct(r rune) bool {
	return (r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef) || r == '“' || r == '”'
}

// japanese are the Japanese words of the English texts written in Han characters only, which their script does not
// tell from Chinese
var japanese = map[string]bool{"日本語": true}

// wordLang is the language of w, a word of Chinese, Japanese or Korean (cjkWord)
func wordLang(w string) string {
	for _, r := range w {
		switch {
		case unicode.Is(unicode.Hangul, r):
			return "ko"
		case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			return "ja"
		}
	}
	if japanese[w] {
		return "ja"
	}
	return "zh-CN"
}

// prompt typesets s, a text shown as it is copied (the prompt for agents): escaped, its URLs broken after slashes.
// The space before Chinese punctuation after a URL is shown, as it is copied: it keeps the punctuation out of the
// URL for the agent that reads it.
func (ts typesetter) prompt(s string) htmltemplate.HTML {
	h := bareURL.ReplaceAllStringFunc(escape(ts.expand(s)), breakURL)
	if ts.zh {
		h = spacedPunct.ReplaceAllString(h, `<span class="nw"> $1</span>`)
	}
	return htmltemplate.HTML(h) //nolint:gosec // escaped
}

// blocks are the blocks of a section, or of the section of an app (id), as the page shows them.
func (ts typesetter) blocks(id string, blocks []block) ([]blockView, error) {
	views := make([]blockView, 0, len(blocks))
	for i, b := range blocks {
		var v blockView
		var err error
		switch {
		case b.Code != "":
			label := ""
			if i > 0 {
				label = fileOf(blocks[i-1].Text)
			}
			v, err = ts.code(b, label)
		case b.Steps != nil:
			v.Kind = "steps"
			v.Items, err = ts.items(b.Steps, "")
		case b.Items != nil:
			v.Kind = listKinds[id]
			if v.Kind == "" {
				v.Kind, v.Class = "list", "dash"
			}
			v.Items, err = ts.items(b.Items, v.Kind)
		default:
			v, err = ts.para(id, b, i > 0 && blocks[i-1].Items != nil)
		}
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

var (
	// a paragraph of links only: [Add to VS Code](…) · [VS Code Insiders](…)
	linksOnly = regexp.MustCompile(`^\[[^\]]+\]\([^)]+\)(?:\s*·\s*\[[^\]]+\]\([^)]+\))*$`)
	link      = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	// a paragraph of a label and a command only: CLI: `{cline.sh}`
	labelledSnippet = regexp.MustCompile("^([^`:：]{1,24})[:：]\\s*`\\{([a-z0-9.-]+\\.sh)\\}`$")
)

func (ts typesetter) para(id string, b block, afterList bool) (blockView, error) {
	if linksOnly.MatchString(b.Text) {
		v := blockView{Kind: "buttons"}
		for _, m := range link.FindAllStringSubmatch(b.Text, -1) {
			href := ts.expand(m[2])
			if err := checkLink(href); err != nil {
				return v, err
			}
			label, err := ts.text(m[1])
			if err != nil {
				return v, err
			}
			v.Links = append(v.Links, linkView{URL: htmltemplate.URL(href), Label: label}) //nolint:gosec // checked
		}
		return v, nil
	}
	if m := labelledSnippet.FindStringSubmatch(b.Text); m != nil {
		return ts.code(block{Code: "{" + m[2] + "}", Lang: langOf(m[2])}, m[1])
	}
	h, err := ts.text(b.Text)
	v := blockView{Kind: "p", HTML: h}
	if id == "try" && afterList {
		v.Class = "callout"
	}
	return v, err
}

func (ts typesetter) items(items []string, kind string) ([]itemView, error) {
	views := make([]itemView, 0, len(items))
	for _, s := range items {
		var v itemView
		var err error
		switch kind {
		case "prompts":
			v.HTML, err = ts.text(s)
			v.Copy = plainText(v.HTML)
		case "rows":
			v, err = ts.row(s)
		case "faq":
			question, answer, ok := cutBold(s)
			if !ok {
				return nil, fmt.Errorf("guide: a question without its **question** in %q", s)
			}
			if v.Label, err = ts.text(question); err == nil {
				v.HTML, err = ts.text(answer)
			}
		default:
			v.HTML, err = ts.text(s)
		}
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

// cutBold cuts **question:** answer into the question, without its colon, and the answer
func cutBold(s string) (question, answer string, ok bool) {
	rest, ok := strings.CutPrefix(s, "**")
	if !ok {
		return "", "", false
	}
	question, answer, ok = strings.Cut(rest, "**")
	return strings.TrimRight(strings.TrimSpace(question), ":："), strings.TrimSpace(answer), ok
}

var (
	// the label of a row: the words before the first colon
	rowLabel = regexp.MustCompile("^([^`*\\[:：]{1,40}?)(?:: |：)(.+)$")
	// a value of commands only: `a`, then `b`
	commands = regexp.MustCompile("^`[^`]+`(?:[^`*\\[]{1,12}`[^`]+`)*$")
	codeSpan = regexp.MustCompile("`([^`]+)`|[^`]+")
)

// row is an item of a list of rows: Label: value, the value as commands to copy if it is only commands.
func (ts typesetter) row(s string) (itemView, error) {
	var v itemView
	m := rowLabel.FindStringSubmatch(s)
	if m == nil {
		h, err := ts.text(s)
		return itemView{HTML: h}, err
	}
	var err error
	if v.Label, err = ts.text(m[1]); err != nil {
		return v, err
	}
	if !commands.MatchString(m[2]) {
		v.HTML, err = ts.text(m[2])
		return v, err
	}
	join := ""
	for _, part := range codeSpan.FindAllStringSubmatch(m[2], -1) {
		if part[1] == "" {
			join = strings.Trim(part[0], " ,，")
			continue
		}
		v.Snips = append(v.Snips, ts.snip(part[1], join))
		join = ""
	}
	return v, nil
}

// snip is the command or configuration code, to copy
func (ts typesetter) snip(code, join string) snipView {
	c := ts.expand(code)
	return snipView{Join: join, HTML: htmltemplate.HTML(breakLong(escape(c))), Copy: c} //nolint:gosec // escaped
}

// other is the table of the other apps.
func (ts typesetter) other(rows [][3]string) ([]otherView, error) {
	views := make([]otherView, 0, len(rows))
	for _, r := range rows {
		var v otherView
		var err error
		if v.App, err = ts.text(r[0]); err != nil {
			return nil, err
		}
		if v.Where, err = ts.text(r[1]); err != nil {
			return nil, err
		}
		// a code first is the command or the configuration, and the rest a note
		if m := codeFirst.FindStringSubmatch(r[2]); m != nil {
			s := ts.snip(m[1], "")
			v.Snip = &s
			r[2] = strings.TrimSpace(m[2])
		}
		if v.What, err = ts.text(r[2]); err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

// file is the name of a file of a snippet: the code of the paragraph before it that names a file of its format
var file = regexp.MustCompile("`([~.$\\w/-]+\\.(?:json|toml))`")

func fileOf(text string) string {
	if m := file.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// code is the block of code of b, labelled with label, the file it goes in, if any.
func (ts typesetter) code(b block, label string) (blockView, error) {
	text := ts.expand(b.Code)
	v := codeView{Kind: b.Lang, Label: label, Copy: text}
	switch b.Lang {
	case "json":
		var indented bytes.Buffer
		if err := json.Indent(&indented, []byte(text), "", "  "); err != nil {
			return blockView{}, fmt.Errorf("guide: %s: %w", b.Code, err)
		}
		v.Copy = indented.String()
	case "sh":
		if label == "" || label == fileOf("`"+label+"`") {
			v.Label = ts.terminal
		}
	case "toml":
	default:
		return blockView{}, fmt.Errorf("guide: a block of %q", b.Lang)
	}
	if v.Label == "" {
		v.Label = strings.ToUpper(b.Lang)
	}
	lines := strings.Split(v.Copy, "\n")
	for i, line := range lines {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		v.Lines = append(v.Lines, lineView{Indent: min(indent/2, 5), HTML: highlight(b.Lang, line), Break: i < len(lines)-1})
	}
	return blockView{Kind: "code", Code: v}, nil
}

var (
	jsonToken  = regexp.MustCompile(`("(?:[^"\\]|\\.)*")(\s*:)?|[{}\[\],:]|[^"{}\[\],:]+`)
	shellToken = regexp.MustCompile(`'[^']*'|"[^"]*"|\S+|\s+`)
	tomlTable  = regexp.MustCompile(`^\[(.*)\]$`)
	tomlPair   = regexp.MustCompile(`^(\S+)(\s*=\s*)(".*")$`)
)

// highlight highlights a line of code of lang: keys (k), strings (s), punctuation (p), the command (c) and its flags
// (f).
func highlight(lang, line string) htmltemplate.HTML {
	span := func(class, s string) string { return `<span class="` + class + `">` + s + "</span>" }
	var b strings.Builder
	switch lang {
	case "json":
		for _, m := range jsonToken.FindAllStringSubmatch(line, -1) {
			switch {
			case m[2] != "":
				b.WriteString(span("k", nowrapWords(escape(m[1]))) + span("p", m[2]))
			case m[1] != "":
				b.WriteString(span("s", breakLong(escape(m[1]))))
			case strings.TrimSpace(m[0]) == "":
				b.WriteString(m[0])
			case strings.ContainsAny(m[0], "{}[],:"):
				b.WriteString(span("p", m[0]))
			default:
				b.WriteString(escape(m[0]))
			}
		}
	case "toml":
		if m := tomlTable.FindStringSubmatch(line); m != nil {
			b.WriteString(span("p", "[") + span("k", nowrapWords(escape(m[1]))) + span("p", "]"))
		} else if m := tomlPair.FindStringSubmatch(line); m != nil {
			b.WriteString(span("k", escape(m[1])) + span("p", m[2]) + span("s", breakLong(escape(m[3]))))
		} else {
			b.WriteString(escape(line))
		}
	default:
		first := true
		for _, tok := range shellToken.FindAllString(line, -1) {
			s := escape(tok)
			switch {
			case strings.TrimSpace(tok) == "":
				b.WriteString(tok)
				continue
			case len(tok) > 24:
				s = breakLong(s)
			default:
				s = nowrapWords(s)
			}
			switch {
			case first:
				b.WriteString(span("c", s))
			case strings.HasPrefix(tok, "'") || strings.HasPrefix(tok, `"`) || strings.HasPrefix(tok, "https://"):
				b.WriteString(span("s", s))
			case strings.HasPrefix(tok, "-"):
				b.WriteString(span("f", s))
			default:
				b.WriteString(s)
			}
			first = false
		}
	}
	return htmltemplate.HTML(b.String()) //nolint:gosec // escaped
}

// nowrapWords keeps the words joined by hyphens in s, escaped, on one line, but for the longest, which may have to
// break
func nowrapWords(s string) string {
	return hyphenWord.ReplaceAllStringFunc(s, func(w string) string {
		if len(w) > 24 {
			return w
		}
		return `<span class="nw">` + w + "</span>"
	})
}

// plainText is the text of h, HTML
func plainText(h htmltemplate.HTML) string {
	return html.UnescapeString(tag.ReplaceAllString(string(h), ""))
}

// splitName cuts the title of an app into its name and what follows in parentheses: Claude, claude.ai, …
func splitName(s string) (name, qualifier string) {
	for _, p := range [][2]string{{" (", ")"}, {"（", "）"}} {
		if i := strings.Index(s, p[0]); i > 0 && strings.HasSuffix(s, p[1]) {
			return s[:i], strings.TrimSuffix(s[i+len(p[0]):], p[1])
		}
	}
	return s, ""
}

// splitSentence cuts the first sentence of s from the rest, which keeps the space between them
func splitSentence(s string) (first, rest string) {
	for _, end := range []string{". ", "。"} {
		if i := strings.Index(s, end); i > 0 {
			i += len(strings.TrimSpace(end))
			return s[:i], s[i:]
		}
	}
	return s, ""
}

// docsText is how the page shows the URL of the documentation of an app: without its scheme
func docsText(u string) string {
	return strings.TrimPrefix(u, "https://")
}
