package guide

import "testing"

func TestInline(t *testing.T) {
	for s, want := range map[string]string{
		"plain & <simple>":                              "plain &amp; &lt;simple&gt;",
		"`a<b` and **bold**":                            "<code>a&lt;b</code> and <strong>bold</strong>",
		"`\"type\": \"http\"`":                          `<code>"type": "http"</code>`,
		"[Add to `X`](https://x.example/?a=1&b=2)":      `<a href="https://x.example/?a=1&amp;b=2">Add to <code>X</code></a>`,
		"[One](cherrystudio://mcp/install?servers=e30)": `<a href="cherrystudio://mcp/install?servers=e30">One</a>`,
		// a bare URL ends before the punctuation of a sentence, a space or Chinese text
		"Go to https://chatgpt.com/plugins, then.": `Go to <a href="https://chatgpt.com/plugins">https://chatgpt.com/plugins</a>, then.`,
		"文档：https://docs.trae.cn/ide/x（中文）":        `文档：<a href="https://docs.trae.cn/ide/x">https://docs.trae.cn/ide/x</a>（中文）`,
		"(see https://a.example/b).":               `(see <a href="https://a.example/b">https://a.example/b</a>).`,
		// in code, a URL stays code
		"`curl https://a.example`": "<code>curl https://a.example</code>",
	} {
		got, err := inline(s)
		if err != nil || string(got) != want {
			t.Errorf("%q: %q, %v; want %q", s, got, err, want)
		}
	}
	for _, s := range []string{"`open", "**open", "[no link]", "[x](javascript:alert(1))", "[x](http://a.example)"} {
		if got, err := inline(s); err == nil {
			t.Errorf("%q: %q", s, got)
		}
	}
}
