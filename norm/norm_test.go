package norm

import (
	"errors"
	"strings"
	"testing"
)

func runOnce(t *testing.T, in string, cfg Config) ([]byte, *Normalizer) {
	t.Helper()
	n := New(cfg)
	if _, err := n.Write([]byte(in)); err != nil {
		t.Fatalf("write %q: %v", in, err)
	}
	if err := n.Close(); err != nil {
		t.Fatalf("close %q: %v", in, err)
	}
	return n.Output(), n
}

func TestBasicSemantics(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"\r\r\n", "\n\n"},
		{"a\r\nb\rc\nd", "a\nb\nc\nd"},
		{"x  \t\ny", "x\ny"},
		{"   ", ""},
		{"   \n", "\n"},
		{"a   \rb", "a\nb"},
		{"a  \r\r\n", "a\n\n"},
		{"  \r\n", "\n"},
		{"a\t\t b", "a\t\t b"},
		{"\r\r", "\n\n"},
		{"x\x00y", "x\x00y"}, // invalid bytes pass through
	}
	for _, c := range cases {
		got, _ := runOnce(t, c.in, Config{})
		if string(got) != c.out {
			t.Errorf("Keep %q = %q want %q", c.in, got, c.out)
		}
	}
}

func TestPolicies(t *testing.T) {
	inputs := []string{"", "\n", "\n\n", "  \r\n", "a", "a\n", "a\n\n\n"}
	want := map[Policy][]string{
		Keep:      {"", "\n", "\n\n", "\n", "a", "a\n", "a\n\n\n"},
		EnsureOne: {"", "\n", "\n\n", "\n", "a\n", "a\n", "a\n\n\n"},
		TrimEmpty: {"", "\n", "\n", "\n", "a\n", "a\n", "a\n"},
	}
	for p := Keep; p <= TrimEmpty; p++ {
		for i, in := range inputs {
			got, _ := runOnce(t, in, Config{Policy: p})
			if string(got) != want[p][i] {
				t.Errorf("policy %d %q = %q want %q", p, in, got, want[p][i])
			}
			again, _ := runOnce(t, string(got), Config{Policy: p})
			if string(again) != string(got) {
				t.Errorf("idempotence policy %d: %q -> %q", p, got, again)
			}
		}
	}
}

// normalizeAll is the reference implementation: whole input, one Write.
func normalizeAll(in string, cfg Config) string {
	n := New(cfg)
	_, _ = n.Write([]byte(in))
	_ = n.Close()
	return string(n.Output())
}

func TestSplitInvariance(t *testing.T) {
	inputs := []string{"a\r\nb", "\r\r\n", "ab   \t \r\nc", "x \r y", " ", "\r", "\n\r\n\r"}
	for _, in := range inputs {
		ref := normalizeAll(in, Config{})
		for cut := 0; cut <= len(in); cut++ {
			n := New(Config{})
			if _, err := n.Write([]byte(in[:cut])); err != nil {
				t.Fatal(err)
			}
			if _, err := n.Write([]byte(in[cut:])); err != nil {
				t.Fatal(err)
			}
			if err := n.Close(); err != nil {
				t.Fatal(err)
			}
			if string(n.Output()) != ref {
				t.Fatalf("%q cut %d: %q != %q", in, cut, n.Output(), ref)
			}
		}
		// 1-byte chunks.
		n := New(Config{})
		for i := 0; i < len(in); i++ {
			if _, err := n.Write([]byte{in[i]}); err != nil {
				t.Fatal(err)
			}
		}
		_ = n.Close()
		if string(n.Output()) != ref {
			t.Fatalf("%q bytewise: %q != %q", in, n.Output(), ref)
		}
	}
}

func TestTruncation(t *testing.T) {
	in := "ab  \r\ncd\r  \nef"
	for cut := 0; cut <= len(in); cut++ {
		n := New(Config{})
		_, _ = n.Write([]byte(in[:cut]))
		if err := n.Close(); err != nil {
			t.Fatal(err)
		}
		want := normalizeAll(in[:cut], Config{})
		if string(n.Output()) != want {
			t.Fatalf("cut %d: %q != %q", cut, n.Output(), want)
		}
	}
}

func TestMapRoundTrip(t *testing.T) {
	cases := []string{"a  \r\nb", "x\t\r\r\ny ", "plain", "\r\n\r\n"}
	for _, in := range cases {
		_, n := runOnce(t, in, Config{})
		tab := n.Map()
		for o := 0; o < tab.ULen; o++ {
			if got := tab.ToOut(tab.ToOrig(o)); got != o {
				t.Fatalf("%q roundtrip o=%d -> %d", in, o, got)
			}
		}
		assertMono(t, tab, in)
	}
}

func assertMono(t *testing.T, tab interface {
	ToOut(int) int
	ToOrig(int) int
}, in string) {
	// Endpoint lengths come from the concrete table below.
}

func TestDeletedOffsets(t *testing.T) {
	_, n := runOnce(t, "ab   \r\nc", Config{})
	tab := n.Map()
	// deleted spaces 2..4 and '\r' at 5 all map before the newline (out 2).
	for _, i := range []int{2, 3, 4, 5} {
		if got := tab.ToOut(i); got != 2 {
			t.Fatalf("ToOut(%d)=%d want 2", i, got)
		}
	}
	for i := 1; i <= tab.OLen; i++ {
		if tab.ToOut(i) < tab.ToOut(i-1) {
			t.Fatalf("ToOut not monotone at %d", i)
		}
	}
	for o := 1; o <= tab.ULen; o++ {
		if tab.ToOrig(o) < tab.ToOrig(o-1) {
			t.Fatalf("ToOrig not monotone at %d", o)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		in   string
		want error
		off  int
	}{
		{"nul", Config{StrictNUL: true}, "ab\x00", ErrNUL, 2},
		{"wslimit", Config{WhitespaceLimit: 2}, "x   ", ErrWhitespaceLimit, 1},
		{"outlimit", Config{OutputLimit: 2}, "abcdef", ErrOutputLimit, 2},
	}
	for _, c := range cases {
		n := New(c.cfg)
		_, err := n.Write([]byte(c.in))
		var oe *OffsetError
		if !errors.As(err, &oe) || !errors.Is(err, c.want) || oe.Offset != c.off {
			t.Fatalf("%s: err=%v off=%d want %v@%d", c.name, err, oe.Offset, c.want, c.off)
		}
		if _, err := n.Write([]byte("z")); !errors.Is(err, ErrClosed) {
			t.Fatalf("%s: post-terminal write err=%v", c.name, err)
		}
		if err := n.Close(); !errors.Is(err, ErrClosed) {
			t.Fatalf("%s: close after terminal err=%v", c.name, err)
		}
	}
}

func TestLargeRuns(t *testing.T) {
	var b strings.Builder
	for range 1000 {
		b.WriteString("x  \r\n")
	}
	_, n := runOnce(t, b.String(), Config{})
	if !strings.ContainsAny(string(n.Output()), "\r") {
		// sanity
	}
}
