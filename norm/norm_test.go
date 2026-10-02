package norm_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ontology/norm"
	"ontology/span"
	"ontology/ws"
)

func run(t *testing.T, in string, opt norm.Options, cuts ...int) (string, *span.Map) {
	t.Helper()
	n := norm.New(opt)
	prev := 0
	for _, c := range append(cuts, len(in)) {
		n.Write([]byte(in[prev:c])) // 这些用例不触发错误
		prev = c
	}
	n.Close()
	return string(n.Output()), n.Map()
}

func TestTable(t *testing.T) {
	P, E, S := norm.Preserve, norm.EnsureOne, norm.StripTrailing
	cases := []struct {
		in   string
		pol  norm.Policy
		want string
	}{
		{"", P, ""}, {"\n", P, "\n"}, {"\n\n", P, "\n\n"}, {"  \r\n", P, "\n"},
		{"", E, ""}, {"\n", E, "\n"}, {"\n\n", E, "\n"}, {"  \r\n", E, "\n"},
		{"", S, ""}, {"\n", S, ""}, {"\n\n", S, ""}, {"  \r\n", S, ""},
		{"\r\n", P, "\n"}, {"\r", P, "\n"}, {"\r\r\n", P, "\n\n"},
		{"a\rb\r\nc\n", P, "a\nb\nc\n"}, {"a  \n", P, "a\n"}, {"a \t b\n", P, "a \t b\n"},
		{" \t\n", P, "\n"}, {"a  ", P, "a"}, {"ab\t\r", P, "ab\n"},
		{"a", E, "a\n"}, {"a\n\n\n", E, "a\n"}, {"a", S, "a\n"}, {"a\n\n\n", S, "a\n"},
		{"a\x00b\xff\n", P, "a\x00b\xff\n"}, // NUL 与非法 UTF-8 原样通过
	}
	for _, c := range cases {
		if got, _ := run(t, c.in, norm.Options{Policy: c.pol}); got != c.want {
			t.Errorf("Normalize(%q,pol=%d)=%q want %q", c.in, c.pol, got, c.want)
		}
	}
}

func TestSplitsAndTruncation(t *testing.T) {
	for _, in := range []string{"\r\na \t \r\n\r\rb  \n\n c \t", " \r ", "\r\r\r", "a  \r\n  \n"} {
		ref, rm := run(t, in, norm.Options{})
		for cut := 0; cut <= len(in); cut++ {
			got, gm := run(t, in, norm.Options{}, cut)
			if got != ref || !slices.Equal(gm.Runs(), rm.Runs()) {
				t.Fatalf("split %q@%d: %q != %q", in, cut, got, ref)
			}
			pre, cuts := in[:cut], []int{}
			for i := 1; i < len(pre); i++ {
				cuts = append(cuts, i)
			}
			got, _ = run(t, pre, norm.Options{}, cuts...)
			want, _ := run(t, pre, norm.Options{})
			if got != want {
				t.Fatalf("truncate %q@%d: %q != %q", in, cut, got, want)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	inputs := []string{"", "\n", "\n\n", "  \r\n", "a \t \r\n\r\rb  ", "\r\r\n", "x\n\n\ny"}
	for _, pol := range []norm.Policy{norm.Preserve, norm.EnsureOne, norm.StripTrailing} {
		for _, in := range inputs {
			once, _ := run(t, in, norm.Options{Policy: pol})
			twice, _ := run(t, once, norm.Options{Policy: pol})
			if once != twice || strings.ContainsRune(once, '\r') {
				t.Fatalf("pol=%d in=%q once=%q twice=%q", pol, in, once, twice)
			}
			for _, ln := range strings.Split(once, "\n") {
				if strings.TrimRight(ln, " \t") != ln {
					t.Fatalf("pol=%d in=%q: trailing WS in %q", pol, in, ln)
				}
			}
		}
	}
}

func TestMapping(t *testing.T) {
	for _, in := range []string{"a  \nb\r\nc \t\r", "", "\n\n", "  \r\nx  ", "a\rb"} {
		out, m := run(t, in, norm.Options{})
		for o := 0; o <= len(out); o++ {
			if m.ToOut(m.ToOrig(o)) != o {
				t.Fatalf("%q: ToOut(ToOrig(%d))=%d", in, o, m.ToOut(m.ToOrig(o)))
			}
			if o > 0 && m.ToOrig(o) < m.ToOrig(o-1) {
				t.Fatalf("%q: ToOrig not monotone at %d", in, o)
			}
		}
		for i := 1; i <= len(in); i++ {
			if m.ToOut(i) < m.ToOut(i-1) {
				t.Fatalf("%q: ToOut not monotone at %d", in, i)
			}
		}
	}
	cases := []struct {
		in      string
		i, want int
	}{
		{"a  \n", 1, 1}, {"a  \n", 2, 1}, // 被删行尾空白 → 行尾 \n 之前
		{"a\r\nb", 1, 1},               // \r\n 的 \r → \n 的位置
		{"ab  ", 2, 2}, {"ab  ", 3, 2}, // 文件尾被删空白 → len(out)
	}
	for _, c := range cases {
		_, m := run(t, c.in, norm.Options{})
		if got := m.ToOut(c.i); got != c.want {
			t.Errorf("ToOut(%q,%d)=%d want %d", c.in, c.i, got, c.want)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		in   string
		opt  norm.Options
		want error
		off  int
	}{
		{"a\x00b", norm.Options{Strict: true}, norm.ErrNUL, 1},
		{"a   \n", norm.Options{MaxPending: 2}, ws.ErrOverflow, 3},
		{"abc", norm.Options{MaxOutput: 2}, norm.ErrOutput, 2},
	}
	for _, c := range cases {
		n := norm.New(c.opt)
		_, err := n.Write([]byte(c.in))
		var at *norm.ErrAt
		if !errors.Is(err, c.want) || !errors.As(err, &at) || at.Off != c.off {
			t.Fatalf("%q: err=%v want %v@%d", c.in, err, c.want, c.off)
		}
		if _, err := n.Write([]byte("x")); !errors.Is(err, norm.ErrClosed) {
			t.Fatalf("%q: write after error=%v want ErrClosed", c.in, err)
		}
	}
	n := norm.New(norm.Options{})
	n.Close()
	if _, err := n.Write([]byte("x")); !errors.Is(err, norm.ErrClosed) {
		t.Fatalf("write after close=%v want ErrClosed", err)
	}
}
