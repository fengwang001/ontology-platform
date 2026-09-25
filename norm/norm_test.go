package norm_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"ontology/norm"
	"ontology/span"
)

func run(in string, cfg norm.Config) (string, *span.Table) {
	n := norm.New(cfg)
	_, err := n.Write([]byte(in))
	if err != nil {
		t := err
		_ = t
	}
	_ = n.Close()
	return string(n.Output()), n.Map()
}

func TestNormalizeCases(t *testing.T) {
	cases := []struct {
		name, in, want string
		policy         norm.Policy
	}{
		{"crlf", "a\r\n", "a\n", norm.Keep},
		{"cr", "a\r", "a\n", norm.Keep},
		{"cr-crlf", "\r\r\n", "\n\n", norm.Keep},
		{"trailing", "a  \t b  \n", "a  \t b\n", norm.Keep},
		{"blank-only", "  \t\n", "\n", norm.Keep},
		{"nul-passthrough", "a\x00b\r", "a\x00b\n", norm.Keep},
		{"keep", "\n\n", "\n\n", norm.Keep},
		{"ensure", "\n\n", "\n", norm.EnsureOne},
		{"trim", "\n\n", "\n", norm.TrimBlank},
	}
	for _, tc := range cases {
		got, _ := run(tc.in, norm.Config{Policy: tc.policy})
		if got != tc.want {
			t.Fatalf("%s: %q != %q", tc.name, got, tc.want)
		}
	}
}

func TestSplitAndTruncation(t *testing.T) {
	in := "ab \r\nx\t\ry  z\t\r\n"
	for _, policy := range []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimBlank} {
		want, _ := run(in, norm.Config{Policy: policy})
		for cut := 0; cut <= len(in); cut++ {
			n := norm.New(norm.Config{Policy: policy})
			if _, err := n.Write([]byte(in[:cut])); err != nil {
				t.Fatal(err)
			}
			if err := n.Close(); err != nil {
				t.Fatal(err)
			}
			truncWant, _ := run(in[:cut], norm.Config{Policy: policy})
			if string(n.Output()) != truncWant {
				t.Fatalf("truncate %d: %q != %q", cut, n.Output(), truncWant)
			}
			if cut < len(in) {
				n = norm.New(norm.Config{Policy: policy})
				if _, err := n.Write([]byte(in[:cut])); err != nil {
					t.Fatal(err)
				}
				if _, err := n.Write([]byte(in[cut:])); err != nil {
					t.Fatal(err)
				}
				if err := n.Close(); err != nil {
					t.Fatal(err)
				}
				if string(n.Output()) != want {
					t.Fatalf("split %d: %q != %q", cut, n.Output(), want)
				}
			}
		}
	}
}

func TestMapping(t *testing.T) {
	in := "a  \r\nb \n"
	out, tab := run(in, norm.Config{Policy: norm.Keep})
	last := 0
	for o := 0; o <= len(out); o++ {
		i := tab.ToOrig(o)
		if got := tab.ToOut(i); got != o {
			t.Fatalf("inverse at %d: orig=%d out=%d", o, i, got)
		}
		if i < last {
			t.Fatal("ToOrig decreased")
		}
		last = i
	}
	for _, i := range []int{1, 2, 3} {
		if got := tab.ToOut(i); got != 1 {
			t.Fatalf("deleted %d -> %d, want 1", i, got)
		}
	}
	prev := -1
	for i := 0; i <= len(in); i++ {
		o := tab.ToOut(i)
		if o < prev {
			t.Fatal("ToOut decreased")
		}
		prev = o
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  norm.Config
		in   string
		want error
		off  int
	}{
		{"nul", norm.Config{StrictNUL: true}, "ab\x00", norm.ErrNUL, 2},
		{"ws", norm.Config{WSBuffer: 2}, "a   ", norm.ErrWSLimit, 3},
		{"output", norm.Config{OutputLimit: 1}, "\n\n", norm.ErrOutputLimit, 2},
	}
	for _, tc := range cases {
		n := norm.New(tc.cfg)
		_, err := n.Write([]byte(tc.in))
		var oe *norm.OffsetError
		if !errors.As(err, &oe) || !errors.Is(err, tc.want) || oe.Offset != tc.off {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if _, err := n.Write([]byte("x")); !errors.Is(err, norm.ErrClosed) {
			t.Fatalf("%s closed: %v", tc.name, err)
		}
	}
}

func TestIdempotent(t *testing.T) {
	ins := []string{"", "\n", "\n\n", "  \r\n", "a \rb\r\nc", "\t\nx"}
	for _, policy := range []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimBlank} {
		for _, in := range ins {
			first, _ := run(in, norm.Config{Policy: policy})
			second, _ := run(first, norm.Config{Policy: policy})
			if first != second || strings.ContainsRune(first, '\r') {
				t.Fatalf("policy %d in %q", policy, in)
			}
		}
	}
}

func TestMappingComplexity(t *testing.T) {
	build := func(lines, width int) string {
		var b strings.Builder
		for i := 0; i < lines; i++ {
			b.WriteString(strings.Repeat("x", width))
			b.WriteString("  \r\n")
		}
		return b.String()
	}
	cases := []struct{ lines, width int }{
		{100000, 80},
		{119047, 80},
	}
	for _, tc := range cases {
		in := build(tc.lines, tc.width)
		out, tab := run(in, norm.Config{})
		limit := int(2*math.Log2(float64(tab.Len())) + 4)
		_ = tab.ToOrig(len(out) - 1)
		if got := tab.LastCheck(); got > limit {
			t.Fatalf("ToOrig checks %d > %d", got, limit)
		}
		_ = tab.ToOut(len(in) - 1)
		if got := tab.LastCheck(); got > limit {
			t.Fatalf("ToOut checks %d > %d", got, limit)
		}
	}
	plain := strings.Repeat("\n", 10_000_000)
	_, tab := run(plain, norm.Config{})
	if tab.Len() > 8 {
		t.Fatalf("identity spans %d", tab.Len())
	}
}
