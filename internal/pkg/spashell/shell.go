// Package spashell fetches the frontend's index.html (the "shell" of the single-page app) and splices page specific
// markup into it: the language of <html>, tags at the end of <head>, and content at the end of the element the app
// mounts on, which the app replaces once it starts.
package spashell

import (
	"bytes"
	"errors"
	"html"
	"regexp"
	"time"
)

// ErrNotShell is returned by Parse for a document that is not the app's index.html.
var ErrNotShell = errors.New("spashell: not the index.html of the app: no element with id app")

var (
	// the app mounts on #app (frontend-v2 src/main.js), which is a <main> in its index.html
	appIDRe    = regexp.MustCompile(`(?i)\bid\s*=\s*["']?app["'\s/>]`)
	htmlTagRe  = regexp.MustCompile(`(?i)<html\b([^>]*)>`)
	langAttrRe = regexp.MustCompile(`(?i)\s+lang\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)
	headEndRe  = regexp.MustCompile(`(?i)</head\s*>`)
	mountRe    = regexp.MustCompile(`(?i)<main\b[^>]*\bid\s*=\s*["']?app["'\s/>]`)
	mountEndRe = regexp.MustCompile(`(?i)</main\s*>`)

	// the tags of <head> each page sets itself
	replacedTagRes = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<title\b[^>]*>.*?</title\s*>`),
		regexp.MustCompile(`(?i)<meta\b[^>]*\b(?:name|property)\s*=\s*["']?` +
			`(?:description|robots|googlebot|og:url|og:type|og:title|og:description|og:image:alt|og:site_name|og:locale|og:locale:alternate|twitter:title|twitter:description|twitter:image:alt)` +
			`(?:["'][^>]*|[\s/][^>]*)?>`),
		regexp.MustCompile(`(?i)<link\b[^>]*\brel\s*=\s*["']?canonical["'\s/>][^>]*>?`),
		regexp.MustCompile(`(?i)<link\b[^>]*\bhreflang\s*=[^>]*>`),
		regexp.MustCompile(`(?is)<script\b[^>]*application/ld\+json[^>]*>.*?</script\s*>`),
	}
)

// Shell is a parsed copy of the app's index.html. It is immutable once parsed.
type Shell struct {
	raw []byte

	// spliceable tells whether the anchors to splice at were found; if not, the shell is served as is
	spliceable bool
	// beforeHTML is everything before the <html> tag
	beforeHTML []byte
	// htmlAttrs are the attributes of the <html> tag but lang, each with the white space before it
	htmlAttrs []byte
	// head is everything after the <html> tag up to </head>, without the tags of replacedTagRes
	head []byte
	// body is everything from </head> up to the end tag of the element the app mounts on
	body []byte
	// tail is everything from the end tag of the element the app mounts on
	tail []byte

	// FetchedAt is when the shell was fetched or revalidated last
	FetchedAt time.Time
	// LastModified is the Last-Modified of the response the shell came with, or when it was fetched without one
	LastModified time.Time

	etag             string
	lastModifiedText string
}

// Parse parses an index.html. It fails only for a document that is not the app's index.html; one whose anchors were
// not found is parsed into a Shell rendered as is.
func Parse(doc []byte) (*Shell, error) {
	if !appIDRe.Match(doc) {
		return nil, ErrNotShell
	}
	s := &Shell{raw: doc}

	htmlTag := htmlTagRe.FindSubmatchIndex(doc)
	headEnd := headEndRe.FindIndex(doc)
	mount := mountRe.FindIndex(doc)
	if htmlTag == nil || headEnd == nil || mount == nil || headEnd[0] < htmlTag[1] || mount[0] < headEnd[1] {
		return s, nil
	}
	mountEnd := mountEndRe.FindIndex(doc[mount[1]:])
	if mountEnd == nil {
		return s, nil
	}
	mountEndAt := mount[1] + mountEnd[0]

	s.beforeHTML = doc[:htmlTag[0]]
	s.htmlAttrs = langAttrRe.ReplaceAll(doc[htmlTag[2]:htmlTag[3]], nil)
	head := doc[htmlTag[1]:headEnd[0]]
	for _, re := range replacedTagRes {
		head = re.ReplaceAll(head, nil)
	}
	s.head = head
	s.body = doc[headEnd[0]:mountEndAt]
	s.tail = doc[mountEndAt:]
	s.spliceable = true
	return s, nil
}

// Spliceable tells whether Render puts the markup given into the shell. If not, it renders the shell as is.
func (s *Shell) Spliceable() bool {
	return s.spliceable
}

// Raw is the index.html as fetched.
func (s *Shell) Raw() []byte {
	return s.raw
}

// Render writes the shell to buf with lang as the language of <html>, head at the end of <head>, and body at the
// end of the element the app mounts on. head and body must be HTML safe; lang is escaped.
func (s *Shell) Render(buf *bytes.Buffer, lang string, head, body []byte) {
	if !s.spliceable {
		buf.Write(s.raw)
		return
	}
	buf.Grow(len(s.raw) + len(head) + len(body) + 32)
	buf.Write(s.beforeHTML)
	buf.WriteString("<html")
	buf.Write(s.htmlAttrs)
	buf.WriteString(` lang="`)
	buf.WriteString(html.EscapeString(lang))
	buf.WriteString(`">`)
	buf.Write(s.head)
	buf.Write(head)
	buf.Write(s.body)
	buf.Write(body)
	buf.Write(s.tail)
}

// revalidated is a copy of s revalidated at t
func (s *Shell) revalidated(t time.Time) *Shell {
	c := *s
	c.FetchedAt = t
	return &c
}
