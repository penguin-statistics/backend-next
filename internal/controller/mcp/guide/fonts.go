package guide

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"strings"
)

// The page is set in Geist and Geist Mono (https://github.com/vercel/geist-font, SIL Open Font License 1.1, see
// fonts/OFL.txt), served from the host of the guide: the page loads nothing from other sites. They are cut from the
// variable fonts of the npm package geist 1.7.2 to the weights the page uses and to Latin, with fontTools:
//
//	U=U+0020-007E,U+00A0-00FF,U+0131,U+0152-0153,U+02C6,U+02DA,U+02DC,U+2013-2014,U+2022,U+20AC,U+2122,U+2190-2193,U+2197,U+2212
//	fonttools varLib.instancer Geist-Variable.ttf wght=400:600 -o sans.ttf
//	fonttools varLib.instancer GeistMono-Variable.ttf wght=400:500 -o mono.ttf
//	fonttools subset sans.ttf --unicodes=$U --layout-features=kern,liga,calt,ccmp,locl,mark,mkmk,tnum,case \
//	  --flavor=woff2 --no-hinting --desubroutinize --name-IDs='*' --output-file=fonts/geist-latin.woff2
//	fonttools subset mono.ttf --unicodes=$U --layout-features=kern,ccmp,locl,mark,mkmk,case \
//	  --flavor=woff2 --no-hinting --desubroutinize --name-IDs='*' --output-file=fonts/geist-mono-latin.woff2
//
// The unicode-range of their @font-face in style.css is that of the cut. Chinese text, and the punctuation the cut
// leaves out (curly quotes, the ellipsis), falls through to the fonts of the system: PingFang SC, Microsoft YaHei,
// Noto Sans CJK SC.

//go:embed fonts
var fontFiles embed.FS

// FontsPath is the path the fonts and their license are served under, on the host of the guide.
const FontsPath = "/fonts/"

// File is a file the page loads from the host of the guide, or links to.
type File struct {
	// Path is its path on the host
	Path        string
	ContentType string
	Body        []byte
	// Immutable tells that Path changes with Body, so it may be cached for good
	Immutable bool
}

// fonts are the fonts of the page by their name in style.css (fonts/<name>), which Render replaces with their path
var fonts = []string{"geist-latin.woff2", "geist-mono-latin.woff2"}

// fontFile is the font name, served at a path made of its name and its hash: a new cut of the font is a new URL.
func fontFile(name string) (File, error) {
	b, err := fontFiles.ReadFile("fonts/" + name)
	if err != nil {
		return File{}, err
	}
	sum := sha256.Sum256(b)
	return File{
		Path:        FontsPath + strings.TrimSuffix(name, ".woff2") + "-" + hex.EncodeToString(sum[:4]) + ".woff2",
		ContentType: "font/woff2",
		Body:        b,
		Immutable:   true,
	}, nil
}

// license is the license of the fonts, which goes with them.
func license() (File, error) {
	b, err := fontFiles.ReadFile("fonts/OFL.txt")
	return File{Path: FontsPath + "OFL.txt", ContentType: "text/plain; charset=utf-8", Body: b}, err
}
