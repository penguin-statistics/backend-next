package seo

import (
	"bytes"
	"embed"
	"html/template"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

// templates escape everything they are given: names come from the database. Never hand them template.HTML.
var templates = template.Must(template.New("").ParseFS(templateFS, "templates/*.gohtml"))

// renderMarkup renders the markup p adds to the head and the body of the shell
func renderMarkup(p *Page) (head, body []byte, err error) {
	var h, b bytes.Buffer
	if err := templates.ExecuteTemplate(&h, "head", p.Head); err != nil {
		return nil, nil, err
	}
	if err := templates.ExecuteTemplate(&b, "body", p.Body); err != nil {
		return nil, nil, err
	}
	return h.Bytes(), b.Bytes(), nil
}
