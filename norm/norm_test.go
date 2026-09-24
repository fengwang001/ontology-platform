package norm_test

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/norm"
)

func normRef(in []byte, p norm.Policy) []byte {
	s := bytes.ReplaceAll(in, []byte("\r\n"), []byte("\n"))
	s = bytes.ReplaceAll(s, []byte("\r"), []byte("\n"))
	var out []byte
	start := 0
	for k := 0; k <= len(s); k++ {
		if k == len(s) || s[k] == '\n' {
			line := s[start:k]
			line = bytes.TrimRight(line, " \t")
			out = append(out, line...)
			if k < len(s) {
				out = append(out, '\n')
			}
			start = k + 1
		}
	}
	switch p {
	case norm.EnsureOne:
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
	case norm.TrimBlank:
		k := 0
		for k < len(out) && out[len(out)-1-k] == '\n' {
			k++
		}
		if k > 0 {
			out = out[:len(out)-k+1]
		}
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
	}
	return out
}

func TestSemantics(t *testing.T) {
	policies := []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimBlank}
	cases := []string{
		"", "\n", "\n\n", "  \r\n", "\r\r\n", "a\rb\r\nc\nd",
		"x  \t \ny", "   \n\t\tab \t\r\n", "no newline   ",
		"mid  dle\tspace", "\n\r\n\r", "a\r\n\r\nb", " \r \n",
		"\x00a\x00", "\xff\xfe bad utf8 \t\r\n", "line \r\r\n two",
	}
	for pi, p := range policies {
		for ci, in := range cases {
			want := normRef([]byte(in), p)
			got, _, err := norm.Normalize([]byte(in), norm.Config{Policy: p})
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("policy %d case %d %q: got %q want %q err=%v", pi, ci, in, got, want, err)
			}
		}
	}
	if out, _, _ := norm.Normalize([]byte("\r\r\n"), norm.Config{}); string(out) != "\n\n" {
		t.Fatalf("\\r\\r\\n -> %q", out)
	}
}

func feed(t *testing.T, in []byte, p norm.Policy, splits []int) []byte {
	t.Helper()
	n := norm.New(norm.Config{Policy: p})
	prev := 0
	for _, s := range splits {
		if _, err := n.Write(in[prev:s]); err != nil {
			t.Fatal(err)
		}
		prev = s
	}
	if _, err := n.Write(in[prev:]); err != nil {
		t.Fatal(err)
	}
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	return n.Output()
}

func TestChunkConsistency(t *testing.T) {
	cases := [][]byte{
		[]byte("ab\r\r\n  \t x \r\n  y\n"),
		[]byte("\r"), []byte("\r\n"), []byte("   \r"), []byte(" \r \n\t"),
		[]byte("z \r"), []byte("a  \r\nb\t"), []byte("\n \n \r\n"),
	}
	policies := []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimBlank}
	rng := rand.New(rand.NewSource(1))
	for _, in := range cases {
		for _, p := range policies {
			want := normRef(in, p)
			for s := 0; s <= len(in); s++ {
				got := feed(t, in, p, []int{s})
				if !bytes.Equal(got, want) {
					t.Fatalf("split at %d policy %d: %q != %q", s, p, got, want)
				}
			}
			for trial := 0; trial < 30; trial++ {
				var splits []int
				for pos := 1; pos < len(in); pos++ {
					if rng.Intn(2) == 0 {
						splits = append(splits, pos)
					}
				}
				if got := feed(t, in, p, splits); !bytes.Equal(got, want) {
					t.Fatalf("random split policy %d: %q != %q", p, got, want)
				}
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	alpha := []byte("ab \t\r\n\x00\xff")
	for _, p := range []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimBlank} {
		for trial := 0; trial < 200; trial++ {
			in := make([]byte, rng.Intn(40))
			for k := range in {
				in[k] = alpha[rng.Intn(len(alpha))]
			}
			first, _, err := norm.Normalize(in, norm.Config{Policy: p})
			if err != nil {
				t.Fatal(err)
			}
			for li, line := range bytes.Split(first, []byte("\n")) {
				trimmed := bytes.TrimRight(line, " \t")
				isLast := li == len(bytes.Split(first, []byte("\n")))-1
				if (len(line) != len(trimmed)) && !(isLast && len(line) == 0) {
					t.Fatalf("output has trailing whitespace: %q", first)
				}
			}
			if bytes.IndexByte(first, '\r') >= 0 {
				t.Fatalf("output contains CR: %q", first)
			}
			second, _, err := norm.Normalize(first, norm.Config{Policy: p})
			if err != nil || !bytes.Equal(first, second) {
				t.Fatalf("policy %d not idempotent: %q -> %q err=%v", p, first, second, err)
			}
		}
	}
}

func TestTruncation(t *testing.T) {
	in := []byte("a  \r\nb\t\r\nc  \rd \r\ne")
	for _, p := range []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimBlank} {
		for cut := 0; cut <= len(in); cut++ {
			n := norm.New(norm.Config{Policy: p})
			if _, err := n.Write(in[:cut]); err != nil {
				t.Fatal(err)
			}
			if err := n.Close(); err != nil {
				t.Fatal(err)
			}
			if got, want := n.Output(), normRef(in[:cut], p); !bytes.Equal(got, want) {
				t.Fatalf("cut %d policy %d: %q != %q", cut, p, got, want)
			}
		}
	}
}

func TestErrors(t *testing.T) {
	n := norm.New(norm.Config{Strict: true})
	_, err := n.Write([]byte("ab\x00"))
	var ne *norm.NULOffsetError
	if !errors.As(err, &ne) || ne.Offset != 2 {
		t.Fatalf("nul: %v", err)
	}
	if !bytes.Equal(n.Output(), []byte("ab")) {
		t.Fatalf("prefix not retained: %q", n.Output())
	}
	if _, err := n.Write([]byte("x")); err != norm.ErrClosed {
		t.Fatalf("write after fatal: %v", err)
	}
	if err := n.Close(); err != norm.ErrClosed {
		t.Fatalf("close after fatal: %v", err)
	}

	n2 := norm.New(norm.Config{MaxWS: 2})
	_, err = n2.Write([]byte("ab   x"))
	var we *norm.WhitespaceLimitError
	if !errors.As(err, &we) || we.Offset != 4 {
		t.Fatalf("ws limit: %v", err)
	}

	n3 := norm.New(norm.Config{MaxOut: 3})
	_, err = n3.Write([]byte("abcde"))
	var oe *norm.OutputLimitError
	if !errors.As(err, &oe) || oe.Offset != 3 {
		t.Fatalf("out limit: %v", err)
	}

	n4 := norm.New(norm.Config{})
	if err := n4.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := n4.Write(nil); err != norm.ErrClosed {
		t.Fatalf("write after close: %v", err)
	}

	// Non-strict mode keeps NUL and invalid UTF-8 verbatim.
	out, _, err := norm.Normalize([]byte("a\x00\xff\t \n"), norm.Config{})
	if err != nil || !bytes.Equal(out, []byte("a\x00\xff\n")) {
		t.Fatalf("lenient: %q %v", out, err)
	}

}
