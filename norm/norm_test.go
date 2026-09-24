package norm

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func run(cfg Config, in []byte) ([]byte, *Normalizer, error) {
	n := New(cfg)
	if _, err := n.Write(in); err != nil {
		return nil, n, err
	}
	err := n.Close()
	return n.Output(), n, err
}

func TestSemantics(t *testing.T) {
	cases := []struct {
		in, want string
		p        Policy
	}{
		{"a\rb", "a\nb", Keep},
		{"a\r\nb", "a\nb", Keep},
		{"\r\r\n", "\n\n", Keep},
		{"a  \tb\n", "a  \tb\n", Keep},
		{"a  \t\nb", "a\nb", Keep},
		{"   \n", "\n", Keep},
		{"a\r\r\n", "a\n\n", Keep},
		{"\rx\n\rz", "\nx\n\nz", Keep},
		{"", "", Keep}, {"", "", One}, {"", "", Trim},
		{"\n", "\n", Keep}, {"\n", "\n", One}, {"\n", "", Trim},
		{"\n\n", "\n\n", Keep}, {"\n\n", "\n", One}, {"\n\n", "", Trim},
		{"  \r\n", "\n", Keep}, {"  \r\n", "\n", One}, {"  \r\n", "", Trim},
		{"a", "a\n", One}, {"a", "a\n", Trim},
		{"a\n\n", "a\n", One}, {"a\n\n", "a\n", Trim},
		{"a\n", "a\n", One},
		{"a\x00b", "a\x00b", Keep},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%q/%d", c.in, c.p), func(t *testing.T) {
			got, _, err := run(Config{Ending: c.p}, []byte(c.in))
			if err != nil || string(got) != c.want {
				t.Fatalf("got %q err=%v want %q", got, err, c.want)
			}
		})
	}
}

func TestIdempotent(t *testing.T) {
	inputs := []string{"", "\n", "\n\n", "a", "a \r\n", "\r\r\nx  \n\n",
		"  \r\n", "mid\ttab  \r\r\ntail \t", "line\n\n\n"}
	for _, p := range []Policy{Keep, One, Trim} {
		for _, in := range inputs {
			first, _, err := run(Config{Ending: p}, []byte(in))
			if err != nil {
				t.Fatal(err)
			}
			second, _, err := run(Config{Ending: p}, first)
			if err != nil || !bytes.Equal(first, second) {
				t.Fatalf("p=%d in=%q: %q -> %q", p, in, first, second)
			}
			if bytes.ContainsRune(first, '\r') {
				t.Fatalf("CR in output %q", first)
			}
		}
	}
}

func TestSplitInvariant(t *testing.T) {
	inputs := []string{"a\r\nb", "\r\r\n", "x  \ry", " \t \n", "a \r\n b  ",
		"\r", "\n", "\r\n", "  \r", "t\t\r", "a\rb\r\nc  "}
	rng := rand.New(rand.NewSource(1))
	for _, p := range []Policy{Keep, One, Trim} {
		for _, in := range inputs {
			base, _, _ := run(Config{Ending: p}, []byte(in))
			for cut := 0; cut <= len(in); cut++ {
				n := New(Config{Ending: p})
				if _, err := n.Write([]byte(in[:cut])); err != nil {
					t.Fatal(err)
				}
				if _, err := n.Write([]byte(in[cut:])); err != nil {
					t.Fatal(err)
				}
				if err := n.Close(); err != nil || !bytes.Equal(n.Output(), base) {
					t.Fatalf("cut=%d in=%q: %q want %q", cut, in, n.Output(), base)
				}
			}
			for rep := 0; rep < 8; rep++ {
				n := New(Config{Ending: p})
				for pos := 0; pos < len(in); {
					step := 1 + rng.Intn(3)
					end := pos + step
					if end > len(in) {
						end = len(in)
					}
					if _, err := n.Write([]byte(in[pos:end])); err != nil {
						t.Fatal(err)
					}
					pos = end
				}
				if err := n.Close(); err != nil || !bytes.Equal(n.Output(), base) {
					t.Fatalf("random split: %q want %q", n.Output(), base)
				}
			}
		}
	}
}

func TestTruncation(t *testing.T) {
	in := "a  \r\nb \rx\t\r\nc  \r"
	for cut := 0; cut <= len(in); cut++ {
		n := New(Config{Ending: Keep})
		if _, err := n.Write([]byte(in[:cut])); err != nil {
			t.Fatal(err)
		}
		if err := n.Close(); err != nil {
			t.Fatal(err)
		}
		want, _, err := run(Config{Ending: Keep}, []byte(in[:cut]))
		if err != nil || !bytes.Equal(n.Output(), want) {
			t.Fatalf("cut=%d got %q want %q", cut, n.Output(), want)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name   string
		cfg    Config
		in     string
		want   error
		offset int
	}{
		{"nul", Config{Strict: true}, "ab\x00", ErrNUL, 2},
		{"wslimit", Config{WSLimit: 2}, "a   ", ErrWSLimit, 3},
		{"outlimit", Config{OutLimit: 2}, "abc", ErrOutLimit, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := New(c.cfg)
			_, err := n.Write([]byte(c.in))
			var oe *OffsetError
			if !errors.As(err, &oe) || oe.Err != c.want || oe.Offset != c.offset {
				t.Fatalf("err=%v", err)
			}
			if _, err := n.Write([]byte("x")); !errors.Is(err, ErrClosed) {
				t.Fatalf("post-state err=%v", err)
			}
			if !errors.Is(n.Close(), ErrClosed) {
				t.Fatal("close after terminal")
			}
		})
	}
}

func TestMapping(t *testing.T) {
	in := []byte("a  \r\nb\t\r\n")
	got, n, err := run(Config{Ending: Keep}, in)
	if err != nil {
		t.Fatal(err)
	}
	for o := 0; o <= len(got); o++ {
		i := n.ToOrig(o)
		if n.ToOut(i) != o {
			t.Fatalf("o=%d i=%d back=%d", o, i, n.ToOut(i))
		}
	}
	for i := 1; i <= len(in); i++ {
		if n.ToOut(i) < n.ToOut(i-1) {
			t.Fatalf("ToOut decrease %d", i)
		}
	}
	// deleted spaces before first newline map onto the newline position
	if n.ToOut(1) != 1 || n.ToOut(2) != 1 || got[1] != '\n' {
		t.Fatalf("deleted-space mapping %d %d %q", n.ToOut(1), n.ToOut(2), got)
	}
	// CR of CRLF shares the output newline offset with the following LF
	if n.ToOut(3) != 1 || n.ToOut(4) != 1 {
		t.Fatalf("CRLF mapping %d %d", n.ToOut(3), n.ToOut(4))
	}
}
