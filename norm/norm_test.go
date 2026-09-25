package norm_test

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"

	"ontology/norm"
	"ontology/span"
)

type refCase struct {
	in  string
	out [3]string
}

var refCases = []refCase{
	{"", [3]string{"", "", ""}},
	{"\n", [3]string{"\n", "\n", ""}},
	{"\n\n", [3]string{"\n\n", "\n", ""}},
	{"  \r\n", [3]string{"\n", "\n", ""}},
	{"a\r\nb", [3]string{"a\nb", "a\nb\n", "a\nb\n"}},
	{"\r\r\n", [3]string{"\n\n", "\n", ""}},
	{"x  \ry", [3]string{"x\ny", "x\ny\n", "x\ny\n"}},
	{"a\t\n b", [3]string{"a\n b", "a\n b\n", "a\n b\n"}},
	{"\r", [3]string{"\n", "\n", ""}},
	{"a\n\n", [3]string{"a\n\n", "a\n", "a\n"}},
	{"a   ", [3]string{"a", "a\n", "a\n"}},
	{"a\n  ", [3]string{"a\n", "a\n", "a\n"}},
	{"a\x00b", [3]string{"a\x00b", "a\x00b\n", "a\x00b\n"}},
	{"\xff\xfe", [3]string{"\xff\xfe", "\xff\xfe\n", "\xff\xfe\n"}},
}

func feed(in string, chunk int, cfg norm.Config) ([]byte, *span.Map, error) {
	n := norm.New(cfg)
	for i := 0; i < len(in); i += chunk {
		j := i + chunk
		if j > len(in) {
			j = len(in)
		}
		if _, err := n.Write([]byte(in[i:j])); err != nil {
			return n.Output(), n.Map(), err
		}
	}
	return n.Output(), n.Map(), n.Close()
}

func TestReferenceTable(t *testing.T) {
	for _, c := range refCases {
		for ei, e := range []norm.Ending{norm.Keep, norm.EnsureOne, norm.TrimEmpty} {
			got, _, err := feed(c.in, len(c.in)+1, norm.Config{Ending: e})
			if err != nil {
				t.Fatalf("%q e=%d: %v", c.in, ei, err)
			}
			if string(got) != c.out[ei] {
				t.Errorf("%q e=%d = %q want %q", c.in, ei, got, c.out[ei])
			}
		}
	}
}

func TestChunkInvariance(t *testing.T) {
	inputs := mixedInputs()
	for _, in := range inputs {
		for _, e := range []norm.Ending{norm.Keep, norm.EnsureOne, norm.TrimEmpty} {
			base, _, err := feed(in, len(in)+1, norm.Config{Ending: e})
			if err != nil {
				t.Fatal(err)
			}
			for ch := 1; ch <= len(in)+1; ch++ {
				got, _, err := feed(in, ch, norm.Config{Ending: e})
				if err != nil {
					t.Fatalf("ch=%d: %v", ch, err)
				}
				if !bytes.Equal(got, base) {
					t.Fatalf("in=%q ch=%d e=%d %q != %q", in, ch, e, got, base)
				}
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, in := range mixedInputs() {
		for _, e := range []norm.Ending{norm.Keep, norm.EnsureOne, norm.TrimEmpty} {
			first, _, err := feed(in, 3, norm.Config{Ending: e})
			if err != nil {
				t.Fatal(err)
			}
			second, _, err := feed(string(first), 5, norm.Config{Ending: e})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, second) {
				t.Fatalf("in=%q e=%d %q -> %q", in, e, first, second)
			}
			if bytes.ContainsRune(first, '\r') {
				t.Fatalf("CR in output: %q", first)
			}
		}
	}
}

func TestTruncation(t *testing.T) {
	// Closing after a prefix must equal normalizing that whole prefix.
	in := "a \r\nx\t\ry   \r\n  z\r"
	for cut := 0; cut <= len(in); cut++ {
		want, _, err := feed(in[:cut], len(in)+1, norm.Config{Ending: norm.Keep})
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := feed(in[:cut], 1, norm.Config{Ending: norm.Keep})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("cut=%d %q != %q", cut, got, want)
		}
	}
}

func TestMapInverseMonotone(t *testing.T) {
	for _, in := range mixedInputs() {
		out, m, err := feed(in, 2, norm.Config{Ending: norm.Keep})
		if err != nil {
			t.Fatal(err)
		}
		prev := -1
		for o := 0; o <= len(out); o++ {
			i := m.ToOrig(o)
			if i < prev {
				t.Fatalf("ToOrig not monotone %q o=%d", in, o)
			}
			prev = i
			if m.ToOut(i) != o {
				t.Fatalf("inverse failed %q o=%d i=%d", in, o, i)
			}
		}
		prevOut := -1
		for i := 0; i <= len(in); i++ {
			o := m.ToOut(i)
			if o < prevOut {
				t.Fatalf("ToOut not monotone %q i=%d", in, i)
			}
			prevOut = o
		}
	}
}

func TestDeletedBytesMapToNewline(t *testing.T) {
	// "ab \r\n": deleted trailing space (2) and CR (3) map to output \n offset 2.
	in := "ab \r\n"
	_, m, err := feed(in, len(in)+1, norm.Config{Ending: norm.Keep})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{2, 3} {
		if got := m.ToOut(i); got != 2 {
			t.Fatalf("ToOut(%d)=%d want 2 (the newline)", i, got)
		}
	}
	// EOF trailing whitespace maps to the output end.
	_, m2, _ := feed("ab  ", 10, norm.Config{Ending: norm.Keep})
	if m2.ToOut(3) != 2 || m2.ToOut(4) != 2 {
		t.Fatal("EOF trailing whitespace did not map to end")
	}
}

func TestErrorsDistinct(t *testing.T) {
	cases := []struct {
		name   string
		cfg    norm.Config
		in     string
		want   error
		offset int
	}{
		{"nul", norm.Config{StrictNUL: true}, "ab\x00c", nil, 2},
		{"pending", norm.Config{MaxPending: 2}, "ab   x", norm.ErrPending, 4},
		{"output", norm.Config{MaxOutput: 2}, "abcdef", norm.ErrOutputMax, 2},
	}
	for _, c := range cases {
		n := norm.New(c.cfg)
		_, err := n.Write([]byte(c.in))
		if err == nil {
			err = n.Close()
		}
		var oe *norm.OffsetError
		if !errors.As(err, &oe) || oe.Offset != c.offset {
			t.Fatalf("%s: err=%v offset", c.name, err)
		}
		if c.want == nil && !norm.IsNUL(err) {
			t.Fatalf("%s: expected NUL", c.name)
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Fatalf("%s: %v not %v", c.name, err, c.want)
		}
		if _, err := n.Write([]byte("x")); !errors.Is(err, norm.ErrClosed) {
			t.Fatalf("%s: post-terminal write = %v", c.name, err)
		}
	}
}

func TestNULKeepsOutput(t *testing.T) {
	n := norm.New(norm.Config{StrictNUL: true})
	_, _ = n.Write([]byte("ab\r\n"))
	_, err := n.Write([]byte("\x00"))
	if !norm.IsNUL(err) {
		t.Fatal(err)
	}
	if string(n.Output()) != "ab\n" {
		t.Fatalf("kept output %q", n.Output())
	}
}

func mixedInputs() []string {
	return []string{
		"a \r\nbb\t\rc  \nd",
		"\r\r\n",
		"   \r\nx \r y",
		strings.Repeat("line  \r\n", 50),
		"abc", "", "\r", "   ", "a\r", "\r b", "a \r b \t c\n",
		"x\x00y\r\nz\t", "\xff bad \r\n",
	}
}

var _ = span.New
var _ = math.Log2
