package ctxtmpl

import "testing"

func TestEscapeHTMLText(t *testing.T) {
	got := escapeHTMLText(`a<b>c&d`)
	want := `a&lt;b&gt;c&amp;d`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNoDoubleEscape(t *testing.T) {
	got := escapeHTMLText(`&lt;<>`)
	want := `&amp;lt;&lt;&gt;`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEscapeDoubleQuoted(t *testing.T) {
	got := escapeAttrValue(`&"<>'`, ctxAttrDouble)
	want := `&amp;&#34;&lt;&gt;'`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEscapeSingleQuoted(t *testing.T) {
	got := escapeAttrValue(`&'<>"`, ctxAttrSingle)
	want := `&amp;&#39;&lt;&gt;"`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEscapeUnquoted(t *testing.T) {
	got := escapeAttrValue("a b\tc\nd=e`f'g\"h&<>", ctxAttrUnquoted)
	want := `a&#32;b&#9;c&#10;d&#61;e&#96;f&#39;g&#34;h&amp;&lt;&gt;`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEscapeComment(t *testing.T) {
	got := escapeComment(`a-->&b`)
	want := `a&#45;&#45;&gt;&amp;b`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
