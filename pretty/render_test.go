package pretty

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func renderOK(t *testing.T, s *Session, doc Doc, width int) (string, []LineInfo) {
	t.Helper()
	out, over, err := s.Render(doc, width)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	return out, over
}

func TestRenderCases(t *testing.T) {
	sp := BreakableSpace()
	soft := BreakableEmpty()
	hard := HardLine()

	cases := []struct {
		name  string
		doc   Doc
		width int
		want  string
		over  []LineInfo
	}{
		{
			name:  "exact fit at equality stays flat",
			doc:   Group(Seq(Text("ab"), sp, Text("cd"))),
			width: 5,
			want:  "ab cd",
		},
		{
			name:  "one column more breaks the group",
			doc:   Group(Seq(Text("ab"), sp, Text("cd"))),
			width: 4,
			want:  "ab\ncd",
		},
		{
			name:  "following content fits: group stays flat",
			doc:   Seq(Group(Seq(Text("ab"), sp, Text("cd"))), Text("efgh")),
			width: 9,
			want:  "ab cdefgh",
		},
		{
			name:  "following content overflows: group breaks",
			doc:   Seq(Group(Seq(Text("ab"), sp, Text("cd"))), Text("efgh")),
			width: 8,
			want:  "ab\ncdefgh",
		},
		{
			name:  "scan stops at breakable of already broken outer context",
			doc:   Seq(Group(Text("abc")), sp, Text("abcdefghij")),
			width: 5,
			want:  "abc\nabcdefghij",
			over:  []LineInfo{{Line: 2, Width: 10}},
		},
		{
			name:  "hard line forces inner and outer groups to break",
			doc:   Group(Seq(Text("a"), sp, Group(Seq(Text("b"), hard, Text("c"))), sp, Text("d"))),
			width: 100,
			want:  "a\nb\nc\nd",
		},
		{
			name:  "nested groups re-decide independently",
			doc:   Group(Seq(Text("a"), sp, Group(Seq(Text("bb"), sp, Text("cc"))))),
			width: 5,
			want:  "a\nbb cc",
		},
		{
			name:  "align uses entry column and indent accumulates on it",
			doc:   Seq(Text("ab"), Align(Indent(2, Group(Seq(Text("cd"), sp, Text("ef")))))),
			width: 5,
			want:  "abcd\n    ef",
			over:  []LineInfo{{Line: 2, Width: 6}},
		},
		{
			name:  "align and indent fit exactly",
			doc:   Seq(Text("ab"), Align(Indent(2, Group(Seq(Text("cd"), sp, Text("ef")))))),
			width: 7,
			want:  "abcd ef",
		},
		{
			name:  "conditional text in flat group",
			doc:   Group(Seq(Text("a"), Cond("BB", "F"), Text("c"))),
			width: 3,
			want:  "aFc",
		},
		{
			name:  "conditional text in broken group",
			doc:   Group(Seq(Text("a"), Cond("BB", "F"), Text("c"))),
			width: 2,
			want:  "aBBc",
			over:  []LineInfo{{Line: 1, Width: 4}},
		},
		{
			name:  "conditional text outside any group counts as broken",
			doc:   Seq(Text("x"), Cond("B", "F")),
			width: 100,
			want:  "xB",
		},
		{
			name:  "double width runes fit exactly",
			doc:   Group(Seq(Text("你好"), sp, Text("x"))),
			width: 6,
			want:  "你好 x",
		},
		{
			name:  "double width runes one column over",
			doc:   Group(Seq(Text("你好"), sp, Text("x"))),
			width: 5,
			want:  "你好\nx",
		},
		{
			name:  "breakable empty flat and broken",
			doc:   Group(Seq(Text("a"), soft, Text("b"))),
			width: 2,
			want:  "ab",
		},
		{
			name:  "breakable empty breaks",
			doc:   Group(Seq(Text("a"), soft, Text("b"))),
			width: 1,
			want:  "a\nb",
		},
		{
			name:  "trailing spaces of text are trimmed",
			doc:   Seq(Text("ab "), hard, Text("z")),
			width: 10,
			want:  "ab\nz",
		},
		{
			name:  "indent-only line becomes empty",
			doc:   Seq(Indent(4, Seq(hard, hard)), Text("z")),
			width: 10,
			want:  "\n\n    z",
		},
		{
			name:  "overwide lines reported in order",
			doc:   Seq(Text("abcd"), hard, Text("xyztu")),
			width: 3,
			want:  "abcd\nxyztu",
			over:  []LineInfo{{Line: 1, Width: 4}, {Line: 2, Width: 5}},
		},
		{
			name:  "top level breakable outside groups breaks",
			doc:   Seq(Text("a"), sp, Text("b")),
			width: 100,
			want:  "a\nb",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, over := renderOK(t, NewSession(), tc.doc, tc.width)
			if out != tc.want {
				t.Errorf("output = %q, want %q", out, tc.want)
			}
			if !reflect.DeepEqual(over, tc.over) {
				t.Errorf("overwide = %+v, want %+v", over, tc.over)
			}
		})
	}
}

func TestFragmentEquivalence(t *testing.T) {
	s := NewSession()
	mustRegister(t, s, "pair", Group(Seq(Text("a"), BreakableSpace(), Text("b"))))
	mustRegister(t, s, "wrap", Seq(Text("["), Ref("pair"), Text("]")))

	viaRef := Seq(Text("<"), Ref("wrap"), Text(">"))
	inlined := Seq(Text("<"), Seq(Text("["), Group(Seq(Text("a"), BreakableSpace(), Text("b"))), Text("]")), Text(">"))

	for _, width := range []int{1, 2, 3, 5, 8, 20, 100} {
		outRef, overRef := renderOK(t, s, viaRef, width)
		outInl, overInl := renderOK(t, s, inlined, width)
		if outRef != outInl {
			t.Errorf("width %d: ref %q != inlined %q", width, outRef, outInl)
		}
		if !reflect.DeepEqual(overRef, overInl) {
			t.Errorf("width %d: overwide %+v != %+v", width, overRef, overInl)
		}
	}
}

func mustRegister(t *testing.T, s *Session, name string, d Doc) {
	t.Helper()
	if err := s.Register(name, d); err != nil {
		t.Fatalf("Register(%q) failed: %v", name, err)
	}
}

func TestRenderErrors(t *testing.T) {
	s := NewSession()
	mustRegister(t, s, "ok", Text("v"))

	// Build a document nested 1001 levels deep (limit is 1000).
	deep := Doc(Text("x"))
	for i := 0; i < 1000; i++ {
		deep = Indent(0, deep)
	}
	// Exactly 1000 levels deep: legal.
	deepOK := Doc(Text("x"))
	for i := 0; i < 999; i++ {
		deepOK = Indent(0, deepOK)
	}

	cases := []struct {
		name  string
		doc   Doc
		width int
		want  error
	}{
		{"width zero", Text("a"), 0, ErrInvalidArgument},
		{"width negative", Text("a"), -3, ErrInvalidArgument},
		{"width too large", Text("a"), 10001, ErrInvalidArgument},
		{"text with newline", Text("a\nb"), 10, ErrInvalidArgument},
		{"cond with newline", Cond("a\nb", "c"), 10, ErrInvalidArgument},
		{"negative indent", Indent(-1, Text("a")), 10, ErrInvalidArgument},
		{"nil document", nil, 10, ErrInvalidArgument},
		{"nil child", Seq(Text("a"), nil), 10, ErrInvalidArgument},
		{"empty ref name", Ref(""), 10, ErrInvalidArgument},
		{"unregistered ref", Ref("nope"), 10, ErrUnregisteredRef},
		{"too deep", deep, 10, ErrTooDeep},
		{"invalid beats unregistered", Seq(Text("a\nb"), Ref("nope")), 10, ErrInvalidArgument},
		{"deep beats unregistered", Indent(0, deep), 10, ErrTooDeep},
		{"width beats deep", deep, 0, ErrInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := s.Render(tc.doc, tc.width)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("depth exactly 1000 is fine", func(t *testing.T) {
		out, _ := renderOK(t, s, deepOK, 10)
		if out != "x" {
			t.Fatalf("out = %q", out)
		}
	})

	t.Run("unregistered ref nested in fragment", func(t *testing.T) {
		_, _, err := s.Render(Group(Ref("missing")), 10)
		if !errors.Is(err, ErrUnregisteredRef) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRegisterErrors(t *testing.T) {
	s := NewSession()

	if err := s.Register("", Text("a")); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("empty name: %v", err)
	}
	if err := s.Register("bad", Text("a\nb")); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("bad text: %v", err)
	}
	if err := s.Register("neg", Indent(-2, Text("a"))); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("negative indent: %v", err)
	}
	if err := s.Register("nil", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("nil doc: %v", err)
	}
	mustRegister(t, s, "f", Text("v"))
	if err := s.Register("f", Text("w")); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("duplicate: %v", err)
	}
	if err := s.Register("g", Ref("unregistered")); !errors.Is(err, ErrUnregisteredRef) {
		t.Errorf("unregistered ref in fragment: %v", err)
	}
	if err := s.Register("self", Ref("self")); !errors.Is(err, ErrUnregisteredRef) {
		t.Errorf("self reference: %v", err)
	}
	// Invalid param has priority over duplicate name.
	if err := s.Register("f", Text("a\nb")); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("invalid beats duplicate: %v", err)
	}

	// Rejected registrations must not change session state.
	if _, _, err := s.Render(Ref("g"), 10); !errors.Is(err, ErrUnregisteredRef) {
		t.Errorf("rejected registration became visible: %v", err)
	}
	if _, _, err := s.Render(Ref("bad"), 10); !errors.Is(err, ErrUnregisteredRef) {
		t.Errorf("rejected registration became visible: %v", err)
	}
	mustRegister(t, s, "g", Text("now ok"))
	out, _ := renderOK(t, s, Ref("g"), 10)
	if out != "now ok" {
		t.Errorf("out = %q", out)
	}
}

func TestOutputTooLarge(t *testing.T) {
	s := NewSession()
	mustRegister(t, s, "f0", Text("aaaa"))
	for i := 1; i <= 22; i++ {
		mustRegister(t, s, fmt.Sprintf("f%d", i),
			Seq(Ref(fmt.Sprintf("f%d", i-1)), Ref(fmt.Sprintf("f%d", i-1))))
	}
	// f22 expands to 4 * 2^22 = 16777216 columns > 1e7.
	_, _, err := s.Render(Ref("f22"), maxLineWidth)
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("err = %v, want ErrOutputTooLarge", err)
	}
	// f21 = 8388608 columns fits the budget and is reported overwide.
	out, over := renderOK(t, s, Ref("f21"), maxLineWidth)
	if len(out) != 4*1<<21 {
		t.Fatalf("len(out) = %d", len(out))
	}
	if !reflect.DeepEqual(over, []LineInfo{{Line: 1, Width: 4 * 1 << 21}}) {
		t.Fatalf("over = %+v", over)
	}
}

func TestRenderDoesNotMutateDocOrSession(t *testing.T) {
	s := NewSession()
	mustRegister(t, s, "frag", Group(Seq(Text("a"), BreakableSpace(), Text("b"))))
	doc := Seq(Text("["), Ref("frag"), Text("]"))
	before := dumpDoc(doc)
	for _, w := range []int{1, 3, 7, 50} {
		renderOK(t, s, doc, w)
	}
	if after := dumpDoc(doc); after != before {
		t.Fatalf("doc mutated: %s -> %s", before, after)
	}
	// The session still holds exactly the one registered fragment.
	if _, _, err := s.Render(Ref("other"), 10); !errors.Is(err, ErrUnregisteredRef) {
		t.Fatalf("session mutated by render: %v", err)
	}
}

// dumpDoc renders a compact S-expression of the tree for logs and checks.
func dumpDoc(d Doc) string {
	var sb strings.Builder
	var rec func(d Doc)
	rec = func(d Doc) {
		switch n := d.(type) {
		case nil:
			sb.WriteString("nil")
		case *textNode:
			fmt.Fprintf(&sb, "%q", n.s)
		case *spaceNode:
			sb.WriteString("sp")
		case *softNode:
			sb.WriteString("soft")
		case *hardNode:
			sb.WriteString("hard")
		case *indentNode:
			fmt.Fprintf(&sb, "(ind %d ", n.n)
			rec(n.body)
			sb.WriteString(")")
		case *alignNode:
			sb.WriteString("(align ")
			rec(n.body)
			sb.WriteString(")")
		case *groupNode:
			sb.WriteString("(grp ")
			rec(n.body)
			sb.WriteString(")")
		case *condNode:
			fmt.Fprintf(&sb, "(cond %q %q)", n.broken, n.flat)
		case *seqNode:
			sb.WriteString("(seq")
			for _, p := range n.parts {
				sb.WriteByte(' ')
				rec(p)
			}
			sb.WriteString(")")
		case *refNode:
			fmt.Fprintf(&sb, "(ref %q)", n.name)
		}
	}
	rec(d)
	return sb.String()
}
