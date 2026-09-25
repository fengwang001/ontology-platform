package norm_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/norm"
)

func runChunked(t *testing.T, in string, size int, cfg norm.Config) []byte {
	t.Helper()
	n := norm.New(cfg)
	for i := 0; i < len(in); i += size {
		j := i + size
		if j > len(in) {
			j = len(in)
		}
		if _, err := n.Write([]byte(in[i:j])); err != nil {
			t.Fatal(err)
		}
	}
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	return n.Output()
}

func TestSemantics(t *testing.T) {
	cases := []struct {
		in string
		p  string // Preserve
		e  string // Ensure
		tr string // Trim
	}{
		{"", "", "", ""},
		{"\n", "\n", "\n", "\n"},
		{"\n\n", "\n\n", "\n", "\n"},
		{"  \r\n", "\n", "\n", "\n"},
		{"\r\r\n", "\n\n", "\n", "\n"},
		{"a  \rb\t\n", "a\nb\n", "a\nb\n", "a\nb\n"},
		{"a b\t c\r\n", "a b\t c\n", "a b\t c\n", "a b\t c\n"},
		{"x \ty\r\nz", "x \ty\nz", "x \ty\nz\n", "x \ty\nz\n"},
		{"  \n\t\r\n", "\n\n", "\n", "\n"},
		{"ab\000cd\r\n", "ab\x00cd\n", "ab\x00cd\n", "ab\x00cd\n"},
		{"\xff\xfe \r\n", "\xff\xfe\n", "\xff\xfe\n", "\xff\xfe\n"},
		{"a\r\rb", "a\n\nb", "a\n\nb\n", "a\n\nb\n"},
	}
	want := func(p, e, tr string, pol norm.Ending) string {
		switch pol {
		case norm.Preserve:
			return p
		case norm.Ensure:
			return e
		default:
			return tr
		}
	}
	for _, c := range cases {
		for _, e := range []norm.Ending{norm.Preserve, norm.Ensure, norm.Trim} {
			got, _, err := norm.Run([]byte(c.in), norm.Config{Ending: e})
			if err != nil {
				t.Fatalf("%q: %v", c.in, err)
			}
			if w := want(c.p, c.e, c.tr, e); string(got) != w {
				t.Errorf("%q policy %d: got %q want %q", c.in, e, got, w)
			}
			if strings.ContainsRune(string(got), '\r') {
				t.Errorf("%q: CR remains", c.in)
			}
		}
	}
}

func TestChunkInvariance(t *testing.T) {
	ins := []string{"a  \r\nb\r\nc  ", "\r\r\nx\t \ry", "  \n  \n", "\n\r\n\r", "z"}
	for _, in := range ins {
		ref, _, err := norm.Run([]byte(in), norm.Config{Ending: norm.Preserve})
		if err != nil {
			t.Fatal(err)
		}
		for size := 1; size <= len(in)+1; size++ {
			if got := runChunked(t, in, size, norm.Config{Ending: norm.Preserve}); string(got) != string(ref) {
				t.Fatalf("in=%q size=%d got %q want %q", in, size, got, ref)
			}
		}
	}
}

func TestTruncation(t *testing.T) {
	in := "ab  \r\ncd\r\ne  \r\nfg"
	for cut := 0; cut <= len(in); cut++ {
		want, _, err := norm.Run([]byte(in[:cut]), norm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		n := norm.New(norm.Config{})
		if _, err := n.Write([]byte(in[:cut])); err != nil {
			t.Fatal(err)
		}
		if err := n.Close(); err != nil {
			t.Fatal(err)
		}
		if string(n.Output()) != string(want) {
			t.Fatalf("cut=%d got %q want %q", cut, n.Output(), want)
		}
	}
}

func TestIdempotent(t *testing.T) {
	ins := []string{"", "\n", "\n\n", "  \r\n", "a  \r\n\n\n", "x\ry\r"}
	for _, e := range []norm.Ending{norm.Preserve, norm.Ensure, norm.Trim} {
		for _, in := range ins {
			one, _, err := norm.Run([]byte(in), norm.Config{Ending: e})
			if err != nil {
				t.Fatal(err)
			}
			two, _, err := norm.Run(one, norm.Config{Ending: e})
			if err != nil {
				t.Fatal(err)
			}
			if string(one) != string(two) {
				t.Fatalf("policy %d %q -> %q -> %q", e, in, one, two)
			}
		}
	}
}

func TestErrors(t *testing.T) {
	type tc struct {
		name string
		cfg  norm.Config
		in   string
		want error
		off  int
		keep string
	}
	cases := []tc{
		{"nul", norm.Config{StrictNUL: true}, "ab\x00c", norm.ErrNUL, 2, "ab"},
		{"ws", norm.Config{MaxPendingWS: 3}, "ab   c", norm.ErrWSBuffer, 5, "ab"},
		{"out", norm.Config{MaxOutput: 2}, "abc", norm.ErrOutputCap, 2, "ab"},
	}
	for _, c := range cases {
		out, _, err := norm.Run([]byte(c.in), c.cfg)
		var oe *norm.OffsetError
		if !errors.As(err, &oe) || !errors.Is(err, c.want) || oe.Offset != c.off {
			t.Fatalf("%s: err=%v off=%v", c.name, err, oe)
		}
		if string(out) != c.keep {
			t.Fatalf("%s: kept %q want %q", c.name, out, c.keep)
		}
		n := norm.New(c.cfg)
		_, _ = n.Write([]byte(c.in))
		if _, err := n.Write([]byte("x")); !errors.Is(err, c.want) {
			t.Fatalf("%s: post-terminal write err=%v", c.name, err)
		}
		if err := n.Close(); !errors.Is(err, c.want) {
			t.Fatalf("%s: close err=%v", c.name, err)
		}
		fresh := norm.New(c.cfg)
		if err := fresh.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := fresh.Write([]byte("x")); !errors.Is(err, norm.ErrClosed) {
			t.Fatalf("%s: post-close write err=%v", c.name, err)
		}
	}
}
