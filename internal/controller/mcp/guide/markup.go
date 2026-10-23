package guide

import (
	"fmt"
	"html"
	htmltemplate "html/template"
	"strings"
)

// inline renders s, a text in the markup of the texts (see text), as HTML: `code`, **bold**, [text](URL) and bare
// https:// URLs. Everything else is escaped. A markup left open, or a link of another scheme than those of checkLink,
// is an error: the texts are ours, and Render fails at startup (and in the tests) rather than show it.
func inline(s string) (htmltemplate.HTML, error) {
	var b strings.Builder
	bold := false
	for i := 0; i < len(s); {
		switch {
		case s[i] == '`':
			end := strings.IndexByte(s[i+1:], '`')
			if end < 0 {
				return "", fmt.Errorf("guide: unclosed ` in %q", s)
			}
			b.WriteString("<code>" + escape(s[i+1:i+1+end]) + "</code>")
			i += end + 2
		case strings.HasPrefix(s[i:], "**"):
			if bold {
				b.WriteString("</strong>")
			} else {
				b.WriteString("<strong>")
			}
			bold = !bold
			i += 2
		case s[i] == '[':
			label, href, n, ok := cutLink(s[i:])
			if !ok {
				return "", fmt.Errorf("guide: [ without a link in %q", s)
			}
			inner, err := inline(label)
			if err != nil {
				return "", err
			}
			if err := checkLink(href); err != nil {
				return "", err
			}
			b.WriteString(`<a href="` + html.EscapeString(href) + `">` + string(inner) + "</a>")
			i += n
		case strings.HasPrefix(s[i:], "https://"):
			u := s[i : i+urlLen(s[i:])]
			b.WriteString(`<a href="` + html.EscapeString(u) + `">` + escape(u) + "</a>")
			i += len(u)
		default:
			b.WriteString(escape(s[i : i+1]))
			i++
		}
	}
	if bold {
		return "", fmt.Errorf("guide: unclosed ** in %q", s)
	}
	return htmltemplate.HTML(b.String()), nil //nolint:gosec // made of escaped text and checked links
}

// escape escapes s as the text of an element (quotes need no escaping there).
func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// cutLink cuts the link [label](href) at the start of s, of n bytes
func cutLink(s string) (label, href string, n int, ok bool) {
	mid := strings.Index(s, "](")
	if mid < 0 {
		return "", "", 0, false
	}
	end := strings.IndexByte(s[mid:], ')')
	if end < 0 {
		return "", "", 0, false
	}
	return s[1:mid], s[mid+2 : mid+end], mid + end + 1, true
}

// urlLen is the length of the bare URL at the start of s: it ends at a space, a quote or a character outside ASCII
// (Chinese punctuation follows URLs without a space), and leaves out the punctuation that ends a sentence.
func urlLen(s string) int {
	n := 0
	for n < len(s) && s[n] > ' ' && s[n] < 0x7f && !strings.ContainsRune(`"<>`+"`", rune(s[n])) {
		n++
	}
	for n > 0 && strings.ContainsRune(".,;:!?)", rune(s[n-1])) {
		n--
	}
	return n
}

// linkSchemes are the schemes of the links of the guide: the web, and the apps that open a link of their own scheme
// only (Cherry Studio, Goose)
var linkSchemes = []string{"https://", "cherrystudio://", "goose://"}

// checkLink fails for a link of another scheme
func checkLink(href string) error {
	for _, scheme := range linkSchemes {
		if strings.HasPrefix(href, scheme) {
			return nil
		}
	}
	return fmt.Errorf("guide: a link to %q, not of %v", href, linkSchemes)
}
