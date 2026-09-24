package norm

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"ontology/span"
)

func run(t *testing.T, in []byte, tr Trailing, opts ...Option) ([]byte, *span.Mapper) {
	t.Helper()
	n := New(append([]Option{WithTrailing(tr)}, opts...)...)
	if _, err := n.Write(in); err != nil {
		t.Fatalf("write: %v", err)
}
	if err := n.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return n.Output(), n.Map()
}

func TestBasics(t *testing.T) {
	cases := []struct {
		name string
		in   string
		keep string
		one  string
		trim string
	}{
		{"crlf", "a\r\n", "a\n", "a\n", "a\n"},
		{"lonecr", "a\rb", "a\nb", "a\nb", "a\nb"},
		{"crcrlf", "\r\r\n", "\n\n", "\n\n", "\n\n"},
		{"trailws", "a  \nb\t \n", "a\nb\n", "a\nb\n", "a\nb\n"},
		{"midws", "a \tb\n", "a \tb\n", "a \tb\n", "a \tb\n"},
		{"blankws", "  \n", "\n", "\n", "\n"},
		{"empty", "", "", "", ""},
		{"nl", "\n", "\n", "\n", ""},
		{"nl2", "\n\n", "\n\n", "\n", ""},
		{"wsnl", "  \r\n", "\n", "\n", "\n"},
		{"residual", "a  ", "a", "a\n", "a\n"},
		{"resws", "  ", "", "", ""},
		{"nul", "a\x00b\n", "a\x00b\n", "a\x00b\n", "a\x00b\n"},
		{"badutf8", "a\xffb\n", "a\xffb\n", "a\xffb\n", "a\xffb\n"},
		{"onlycr", "\r", "\n", "\n", ""},
	}
	want := func(c struct {
		name       string
		in, keep, one, trim string
	}, tr Trailing) string {
		if tr == Keep {
			return c.keep
		}
		if tr == EnsureOne {
			return c.one
		}
		return c.trim
	}
	for _, c := range cases {
		for _, tr := range []Trailing{Keep, EnsureOne, TrimBlank} {
			got, _ := run(t, []byte(c.in), tr)
			if string(got) != want(c, tr) {
				t.Errorf("%s/%v: got %q want %q", c.name, tr, got, want(c, tr))
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	inputs := []string{"", "\n", "\n\n", "  \r\n", "a\r\rb\t \nx", "\r\r\n", "a  ", " \t ", "x\n\n\n"}
	for _, tr := range []Trailing{Keep, EnsureOne, TrimBlank} {
		for _, in := range inputs {
			first, _ := run(t, []byte(in), tr)
			second, _ := run(t, first, tr)
			if !bytes.Equal(first, second) {
				t.Errorf("idempotency %q/%v: %q != %q", in, tr, first, second)
			}
			if bytes.ContainsRune(first, '\r') {
				t.Errorf("cr remains in %q", first)
			}
		}
	}
}

func TestChunkInvariant(t *testing.T) {
	inputs := []string{"\r\r\n", "a \r\n b\t\r x\r", "  \r\n  ", "\r", "a\r", "\r\n", " \t \r \n"}
	for _, in := range inputs {
		ref, refm := run(t, []byte(in), Keep)
		for cut := 0; cut <= len(in); cut++ {
			n := New()
			if _, err := n.Write([]byte(in[:cut])); err != nil {
				t.Fatal(err)
			}
			if _, err := n.Write([]byte(in[cut:])); err != nil {
				t.Fatal(err)
			}
			if err := n.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(n.Output(), ref) {
				t.Errorf("cut %d of %q: %q != %q", cut, in, n.Output(), ref)
			}
			checkMap(t, n.Map(), refm, fmt.Sprintf("cut%d", cut))
		}
	}
}

func checkMap(t *testing.T, got, ref *span.Mapper, tag string) {
	t.Helper()
	if got.OutLen() != ref.OutLen() || got.OrigLen() != ref.OrigLen() {
		t.Fatalf("%s lengths", tag)
	}
	prev := -1
	for o := 0; o <= ref.OutLen(); o++ {
		if r := got.ToOrig(o); r != ref.ToOrig(o) {
			t.Errorf("%s ToOrig(%d)=%d want %d", tag, o, r, ref.ToOrig(o))
		}
		if got.ToOrig(o) < prev {
			t.Errorf("%s ToOrig not monotone", tag)
		}
		prev = got.ToOrig(o)
	}
	prev = -1
	for i := 0; i <= ref.OrigLen(); i++ {
		if g := got.ToOut(i); g != ref.ToOut(i) {
			t.Errorf("%s ToOut(%d)=%d want %d", tag, i, g, ref.ToOut(i))
		}
		if got.ToOut(i) < prev {
			t.Errorf("%s ToOut not monotone", tag)
		}
		prev = got.ToOut(i)
	}
}

func TestMappingRoundTrip(t *testing.T) {
	inputs := []string{"a  \r\nb\rx\t\n", "\r\r\n", "   ", "a\r\n", "\n\nx "}
	for _, in := range inputs {
		_, m := run(t, []byte(in), Keep)
		for o := 0; o < m.OutLen(); o++ {
			if g := m.ToOut(m.ToOrig(o)); g != o {
				t.Errorf("%q ToOut(ToOrig(%d))=%d", in, o, g)
			}
		}
	}
}

func TestDeletedOffsets(t *testing.T) {
	// "a  \r\n": bytes a,sp,sp,\r,\n -> output "a\n" offsets 0,1.
	_, m := run(t, []byte("a  \r\n"), Keep)
	if g := m.ToOut(1); g != 1 {
		t.Errorf("space ToOut=%d want 1 (before newline)", g)
	}
	if g := m.ToOut(3); g != 1 {
		t.Errorf("cr ToOut=%d want 1", g)
	}
	if g := m.ToOrig(1); g != 4 {
		t.Errorf("newline ToOrig=%d want 4", g)
	}
}

func TestErrors(t *testing.T) {
	n := New(WithStrictNUL())
	err := n.Write([]byte("ab\x00"))
	var oe *OffsetError
	if !errors.As(err, &oe) || !errors.Is(err, ErrNUL) || oe.Offset != 2 {
		t.Fatalf("nul: %v", err)
	}
	if !bytes.Equal(n.Output(), []byte("ab")) {
		t.Fatalf("partial output lost: %q", n.Output())
	}
	if err := n.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after fail: %v", err)
	}

	n2 := New(WithWhitespaceLimit(2))
	if err := n2.Write([]byte("   ")); !errors.Is(err, ErrWhitespaceLimit) {
		t.Fatalf("ws limit: %v", err)
	}
	n3 := New(WithOutputLimit(2))
	if err := n3.Write([]byte("abc")); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("out limit: %v", err)
	}
	// Limits must not fire when the whitespace is later resolved as trailing.
	n4 := New(WithWhitespaceLimit(2))
	if _, err := n4.Write([]byte(" x ")); err != nil {
		t.Fatalf("resolved ws: %v", err)
	}
	if err := n4.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTruncation(t *testing.T) {
	in := "a\r\nb \rc  \r\nd"
	for cut := 0; cut <= len(in); cut++ {
		n := New()
		if _, err := n.Write([]byte(in[:cut])); err != nil {
			t.Fatal(err)
		}
		if err := n.Close(); err != nil {
			t.Fatal(err)
		}
		ref, _ := run(t, []byte(in[:cut]), Keep)
		if !bytes.Equal(n.Output(), ref) {
			t.Errorf("cut %d: %q != %q", cut, n.Output(), ref)
		}
	}
}
