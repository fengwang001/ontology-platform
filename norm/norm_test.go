package norm_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/norm"
	"ontology/span"
)

func run(t *testing.T, in string, cfg norm.Config) (string, *span.Map) {
	t.Helper()
	n := norm.New(cfg)
	if _, err := n.Write([]byte(in)); err != nil {
		t.Fatalf("Write(%q): %v", in, err)
	}
	if err := n.Close(); err != nil {
		t.Fatalf("Close(%q): %v", in, err)
	}
	return string(n.Output()), n.Map()
}

func mapEq(a, b *span.Map) bool {
	if a.Orig != b.Orig || a.Out != b.Out || a.Appended != b.Appended {
		return false
	}
	for o := 0; o <= a.Out; o++ {
		if a.ToOrig(o) != b.ToOrig(o) {
			return false
		}
	}
	for i := 0; i <= a.Orig; i++ {
		if a.ToOut(i) != b.ToOut(i) {
			return false
		}
	}
	return true
}

func TestNormalizeTable(t *testing.T) {
	cases := []struct {
		in   string
		want [3]string // Preserve, EnsureOne, Collapse
	}{
		{"", [3]string{"", "", ""}},
		{"\n", [3]string{"\n", "\n", "\n"}},
		{"\n\n", [3]string{"\n\n", "\n", "\n"}},
		{"  \r\n", [3]string{"\n", "\n", "\n"}},
		{"a", [3]string{"a", "a\n", "a"}},
		{"a\n\nb", [3]string{"a\n\nb", "a\n\nb\n", "a\n\nb"}},
		{"a\r\r\nb", [3]string{"a\n\nb", "a\n\nb", "a\n\nb"}},
		{"a  \n  \nend \t\r\n", [3]string{"a\n\nend\n", "a\n\nend\n", "a\n\nend\n"}},
		{"a\r", [3]string{"a\n", "a\n", "a\n"}},
		{"a  ", [3]string{"a", "a\n", "a"}},
		{"\x00b\xffa", [3]string{"\x00b\xffa", "\x00b\xffa\n", "\x00b\xffa"}},
	}
	for _, c := range cases {
		for pol, want := range c.want {
			got, _ := run(t, c.in, norm.Config{Policy: norm.Policy(pol)})
			if got != want {
				t.Errorf("N(%q, policy=%d) = %q, want %q", c.in, pol, got, want)
			}
		}
	}
}

const mix = "a  \r\nb\rc  \n\n\r\nd  "

func TestSplitAndTruncation(t *testing.T) {
	for pol := 0; pol < 3; pol++ {
		cfg := norm.Config{Policy: norm.Policy(pol)}
		whole, wm := run(t, mix, cfg)
		for i := 0; i <= len(mix); i++ {
			n := norm.New(cfg)
			n.Write([]byte(mix[:i]))
			n.Write([]byte(mix[i:]))
			n.Close()
			if string(n.Output()) != whole || !mapEq(n.Map(), wm) {
				t.Fatalf("split at %d (policy %d) differs", i, pol)
			}
			want, _ := run(t, mix[:i], cfg)
			m := norm.New(cfg)
			for j := 0; j < i; j++ {
				m.Write([]byte{mix[j]})
			}
			m.Close()
			if string(m.Output()) != want {
				t.Fatalf("truncate at %d (policy %d) differs", i, pol)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, in := range []string{mix, "", "\n", "a  ", "a\n\n\n", "  \r\n"} {
		for pol := 0; pol < 3; pol++ {
			cfg := norm.Config{Policy: norm.Policy(pol)}
			x1, _ := run(t, in, cfg)
			x2, _ := run(t, x1, cfg)
			if x1 != x2 || strings.ContainsRune(x1, '\r') {
				t.Fatalf("not idempotent/clean: N(%q)=%q N(N)=%q (policy %d)", in, x1, x2, pol)
			}
			for _, line := range strings.Split(x1, "\n") {
				if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
					t.Fatalf("trailing ws in %q (policy %d)", x1, pol)
				}
			}
		}
	}
}

func TestMapping(t *testing.T) {
	for _, in := range []string{"a  \r\nb\rc\n\n", "a\n\n  ", "a  ", "x"} {
		_, m := run(t, in, norm.Config{Policy: norm.EnsureOne})
		for o := 0; o < m.Out; o++ {
			if m.ToOut(m.ToOrig(o)) != o {
				t.Fatalf("roundtrip failed at %d for %q", o, in)
			}
		}
		for o := 1; o <= m.Out; o++ {
			if m.ToOrig(o) < m.ToOrig(o-1) {
				t.Fatalf("ToOrig not monotone at %d for %q", o, in)
			}
		}
		for i := 1; i <= m.Orig; i++ {
			if m.ToOut(i) < m.ToOut(i-1) {
				t.Fatalf("ToOut not monotone at %d for %q", i, in)
			}
		}
	}
	cases := []struct{ in string; i, out int }{
		{"a  \n", 1, 1}, {"a  \n", 2, 1}, {"a\r\nb", 1, 1}, {"a\n\n  ", 3, 3}, {"a\n\n  ", 4, 3},
	}
	for _, c := range cases {
		_, m := run(t, c.in, norm.Config{})
		if got := m.ToOut(c.i); got != c.out {
			t.Errorf("ToOut(%q, %d) = %d, want %d", c.in, c.i, got, c.out)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		cfg  norm.Config
		in   string
		err  error
		orig int
	}{
		{norm.Config{Strict: true}, "ab\x00c", norm.ErrNUL, 2},
		{norm.Config{WSLimit: 2}, "a   \n", norm.ErrWSLimit, 3},
		{norm.Config{OutLimit: 2}, "abc", norm.ErrOutLimit, 2},
	}
	sentinels := []error{norm.ErrNUL, norm.ErrWSLimit, norm.ErrOutLimit, norm.ErrClosed}
	for _, c := range cases {
		n := norm.New(c.cfg)
		_, err := n.Write([]byte(c.in))
		var ne *norm.Error
		if !errors.Is(err, c.err) || !errors.As(err, &ne) || ne.Orig != c.orig {
			t.Fatalf("got %v, want %v at %d", err, c.err, c.orig)
		}
		for _, s := range sentinels {
			if s != c.err && errors.Is(err, s) {
				t.Fatalf("%v not distinguishable from %v", err, s)
			}
		}
		if _, err := n.Write([]byte("x")); !errors.Is(err, norm.ErrClosed) {
			t.Fatalf("write after error: %v", err)
		}
	}
	n := norm.New(norm.Config{})
	n.Close()
	if _, err := n.Write([]byte("x")); !errors.Is(err, norm.ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
}
