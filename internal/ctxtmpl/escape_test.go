package ctxtmpl

import "testing"

func TestEscapeValue(t *testing.T) {
	cases := []struct {
		name string
		kind contextKind
		in   string
		want string
	}{
		{"text basic", ctxText, `<a & b>`, "&lt;a &amp; b&gt;"},
		{"text no double escape", ctxText, `<`, "&lt;"},
		{"already escaped input", ctxText, "&lt;", "&amp;lt;"},
		{"double quote attr", ctxAttrDouble, `"&<>`, "&quot;&amp;&lt;&gt;"},
		{"double attr keeps single quote", ctxAttrDouble, `'`, `'`},
		{"single quote attr", ctxAttrSingle, `'&<>`, "&#39;&amp;&lt;&gt;"},
		{"unquoted spaces", ctxAttrUnquoted, "a b\tc", "a&#32;b&#9;c"},
		{"unquoted specials", ctxAttrUnquoted, "=`'\"", "&#61;&#96;&#39;&quot;"},
		{"unquoted newline", ctxAttrUnquoted, "\n\r", "&#10;&#13;"},
		{"comment hyphen", ctxComment, "a-->b", "a&#45;&#45;>b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := escapeValue(tc.in, context{kind: tc.kind})
			if got != tc.want {
				t.Fatalf("escapeValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEscapeValuePassesUTF8(t *testing.T) {
	in := "héllo→世界"
	got := escapeValue(in, context{kind: ctxText})
	if got != in {
		t.Fatalf("utf8 altered: got %q", got)
	}
}
