package norm_test

import (
	"bytes"
	"errors"
	"testing"

	"ontology/norm"
	"ontology/span"
)

func normalize(t *testing.T, in string, opt norm.Options) ([]byte, *span.Map) {
	t.Helper()
	out, m, err := norm.Normalize([]byte(in), opt)
	if err != nil {
		t.Fatalf("normalize %q: %v", in, err)
	}
	return out, m
}

func TestLineEndingsAndWS(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\rb\nc\r\n", "a\nb\nc\n"},
		{"\r\r\n", "\n\n"},
		{"a  \t\nb", "a\nb"},
		{"a\tb  \n", "a\tb\n"},
		{"   \n", "\n"},
		{"  \r\n", "\n"},
		{"x\ry  \rz\t\r\n", "x\ny\nz\t\n"},
		{"no newline", "no newline"},
		{"a  ", "a"},
		{"  ", ""},
		{"\x00\xff\r\nx \n", "\x00\xff\nx\n"},
	}
	for _, c := range cases {
		out, _ := normalize(t, c.in, norm.Options{Policy: norm.Keep})
		if string(out) != c.want {
			t.Errorf("Keep %q = %q, want %q", c.in, out, c.want)
		}
	}
}

func TestPolicyTable(t *testing.T) {
	ins := []string{"", "\n", "\n\n", "  \r\n", "a", "a\n\n", "a\n\n\n"}
	want := [][]string{
		{"", "", ""},
		{"\n", "\n", "\n"},
		{"\n\n", "\n", "\n"},
		{"\n", "\n", "\n"},
		{"a", "a\n", "a\n"},
		{"a\n\n", "a\n", "a\n"},
		{"a\n\n\n", "a\n", "a\n"},
	}
	pols := []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimBlank}
	for i, in := range ins {
		for j, p := range pols {
			out, _ := normalize(t, in, norm.Options{Policy: p})
			if string(out) != want[i][j] {
				t.Errorf("in=%q pol=%d got %q want %q", in, p, out, want[i][j])
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	ins := []string{"", "\r\r\n", "a  \r\nb\t \rc\r\n\n", "   ", "x", "\n\n\n", "\xff\x00 \r"}
	pols := []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimBlank}
	for _, in := range ins {
		for _, p := range pols {
			opt := norm.Options{Policy: p}
			first, _ := normalize(t, in, opt)
			second, _ := normalize(t, string(first), opt)
			if !bytes.Equal(first, second) {
				t.Errorf("not idempotent pol=%d %q -> %q -> %q", p, in, first, second)
			}
		}
	}
}

func TestAllCutsIdentical(t *testing.T) {
	ins := []string{"a\r\nb", "\r\r\n", "x  \r\ny\t", "  \r", "\t\t", "a\rb\rc"}
	pols := []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimBlank}
	for _, in := range ins {
		for _, p := range pols {
			opt := norm.Options{Policy: p}
			wantOut, wantMap, err := norm.Normalize([]byte(in), opt)
			if err != nil {
				t.Fatal(err)
			}
			for cut := 0; cut <= len(in); cut++ {
				n := norm.New(opt)
				if _, err := n.Write([]byte(in[:cut])); err != nil {
					t.Fatal(err)
				}
				if _, err := n.Write([]byte(in[cut:])); err != nil {
					t.Fatal(err)
				}
				if err := n.Close(); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(n.Output(), wantOut) || !sameMap(n.Map(), wantMap) {
					t.Fatalf("cut=%d in=%q pol=%d mismatch", cut, in, p)
				}
			}
			// one byte at a time
			n := norm.New(opt)
			for _, b := range []byte(in) {
				if _, err := n.Write([]byte{b}); err != nil {
					t.Fatal(err)
				}
			}
			if err := n.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(n.Output(), wantOut) || !sameMap(n.Map(), wantMap) {
				t.Fatalf("byte-at-time in=%q mismatch", in)
			}
		}
	}
}

func sameMap(a, b *span.Map) bool {
	sa, sb := a.Segs(), b.Segs()
	if len(sa) != len(sb) || a.ILen() != b.ILen() || a.OLen() != b.OLen() {
		return false
	}
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

func TestMapRoundTrip(t *testing.T) {
	ins := []string{"a  \r\nb\r", "\t\nx y \n", "\r\r\n", "plain", "  "}
	for _, in := range ins {
		out, m := normalize(t, in, norm.Options{Policy: norm.Keep})
		for o := int64(0); o <= m.OLen(); o++ {
			i := m.ToOrig(o)
			if got := m.ToOut(i); got != o {
				t.Fatalf("in=%q o=%d i=%d ToOut=%d", in, o, i, got)
			}
		}
		var lastI, lastO int64
		for i := int64(0); i <= m.ILen(); i++ {
			o := m.ToOut(i)
			if o < lastO {
				t.Fatalf("ToOut not monotonic in=%q i=%d", in, i)
			}
			lastO = o
		}
		for o := int64(0); o <= m.OLen(); o++ {
			i := m.ToOrig(o)
			if i < lastI {
				t.Fatalf("ToOrig not monotonic in=%q o=%d", in, o)
			}
			lastI = i
		}
		if len(out) > 0 {
			_ = out
		}
	}
}

func TestDeletedByteMapping(t *testing.T) {
	// "ab \r\n": the \r maps to the output \n; trailing spaces map before it.
	in := "ab \r\n"
	out, m := normalize(t, in, norm.Options{Policy: norm.Keep})
	if string(out) != "ab\n" {
		t.Fatalf("out=%q", out)
	}
	// original offsets: a=0 b=1 sp=2 \r=3 \n=4 ; output: a=0 b=1 \n=2
	if got := m.ToOut(2); got != 2 {
		t.Errorf("space ToOut=%d want 2 (before newline)", got)
	}
	if got := m.ToOut(3); got != 2 {
		t.Errorf("\\r ToOut=%d want 2", got)
	}
	if got := m.ToOut(4); got != 2 {
		t.Errorf("\\n ToOut=%d want 2", got)
	}
	if got := m.ToOrig(2); got != 4 {
		t.Errorf("ToOrig(2)=%d want 4", got)
	}
}

func TestTruncationEveryByte(t *testing.T) {
	in := "a  \r\nb\t \rc\r"
	pols := []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimBlank}
	for _, p := range pols {
		opt := norm.Options{Policy: p}
		for k := 0; k <= len(in); k++ {
			want, _, err := norm.Normalize([]byte(in[:k]), opt)
			if err != nil {
				t.Fatal(err)
			}
			n := norm.New(opt)
			if _, err := n.Write([]byte(in[:k])); err != nil {
				t.Fatal(err)
			}
			if err := n.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(n.Output(), want) {
				t.Fatalf("pol=%d trunc=%d got %q want %q", p, k, n.Output(), want)
			}
		}
	}
}

func TestErrors(t *testing.T) {
	var nul *norm.NULError
	if _, _, err := norm.Normalize([]byte("ab\x00"), norm.Options{StrictNUL: true}); !errors.As(err, &nul) || nul.Off != 2 || !errors.Is(err, norm.ErrNUL) {
		t.Fatalf("nul err=%v", err)
	}
	// output already produced before NUL is retained; terminal afterwards
	n := norm.New(norm.Options{StrictNUL: true})
	if _, err := n.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Write([]byte{'\x00'}); !errors.Is(err, norm.ErrNUL) {
		t.Fatalf("want ErrNUL got %v", err)
	}
	if string(n.Output()) != "ab" {
		t.Fatalf("retained output=%q", n.Output())
	}
	if _, err := n.Write([]byte("c")); !errors.Is(err, norm.ErrClosed) {
		t.Fatalf("post-error write=%v", err)
	}
	if err := n.Close(); !errors.Is(err, norm.ErrNUL) {
		t.Fatalf("close after error=%v", err)
	}
	// ws buffer limit: WSBuffer=2, run "   " rejected at 3rd byte
	_, _, err := norm.Normalize([]byte("x   \n"), norm.Options{WSBuffer: 2})
	if !errors.Is(err, norm.ErrWSBuffer) {
		t.Fatalf("ws err=%v", err)
	}
	// output limit
	_, _, err = norm.Normalize([]byte("abcd"), norm.Options{MaxOutput: 2})
	var oe *norm.OutputError
	if !errors.As(err, &oe) || oe.Limit != 2 || !errors.Is(err, norm.ErrOutput) {
		t.Fatalf("output err=%v", err)
	}
}

func TestNULPassthroughNonStrict(t *testing.T) {
	out, _ := normalize(t, "a\x00b\r\n", norm.Options{})
	if string(out) != "a\x00b\n" {
		t.Fatalf("%q", out)
	}
}
