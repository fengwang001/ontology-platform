package norm_test

import (
	"bytes"
	"errors"
	"testing"

	"ontology/norm"
)

func TestBasic(t *testing.T) {
	cases := []struct {
		name string
		in   string
		mode norm.Trailing
		want string
	}{
		{"crlf", "a\r\nb", norm.Keep, "a\nb"},
		{"cr only", "a\rb", norm.Keep, "a\nb"},
		{"cr cr lf", "\r\r\n", norm.Keep, "\n\n"},
		{"trailing ws", "a b  \t\nc", norm.Keep, "a b\nc"},
		{"ws only line", "   \nx", norm.Keep, "\nx"},
		{"keep blank", "\n\n", norm.Keep, "\n\n"},
		{"ensure add", "x", norm.EnsureOne, "x\n"},
		{"ensure empty", "", norm.EnsureOne, ""},
		{"ensure has nl", "x\n", norm.EnsureOne, "x\n"},
		{"ensure many", "x\n\n", norm.EnsureOne, "x\n\n"},
		{"trim many", "x\n\n\n", norm.Trim, "x\n"},
		{"trim none", "x", norm.Trim, "x\n"},
		{"trim blank", "   \r\n", norm.Trim, "\n"},
		{"trim empty", "", norm.Trim, ""},
		{"mid ws kept", "a\t b \tc", norm.Trim, "a\t b \tc\n"},
		{"nul passthrough", "a\x00b\n", norm.Keep, "a\x00b\n"},
		{"invalid utf8", "a\xff\nb  \n", norm.Keep, "a\xff\nb\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := norm.Normalize([]byte(tc.in), norm.Config{Mode: tc.mode})
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != tc.want {
				t.Fatalf("got %q want %q", out, tc.want)
			}
		})
	}
}

func TestSplits(t *testing.T) {
	inputs := []string{"\r\r\nx  \r\ny\t\r\nz", "a \r b\t\n", "  ", "\r", "x\n\r\ny\r"}
	for _, in := range inputs {
		ref, refMap, err := norm.Normalize([]byte(in), norm.Config{Mode: norm.Trim})
		if err != nil {
			t.Fatal(err)
		}
		for cut := 0; cut <= len(in); cut++ {
			n := norm.New(norm.Config{Mode: norm.Trim})
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
				t.Fatalf("cut=%d got %q want %q", cut, n.Output(), ref)
			}
			if n.Map().Segs() != refMap.Segs() {
				t.Fatalf("cut=%d segs differ", cut)
			}
		}
	}
}

func TestMapInverse(t *testing.T) {
	in := []byte("ab  \r\ncd\t\n e \rf  \r\r\nzz")
	m := norm.New(norm.Config{Mode: norm.Keep})
	if _, err := m.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	mp := m.Map()
	for o := 0; o <= mp.OutLen(); o++ {
		if got := mp.ToOut(mp.ToOrig(o)); got != o {
			t.Fatalf("inverse at %d: got %d", o, got)
		}
	}
	prev := -1
	for i := 0; i <= mp.OrigLen(); i++ {
		if got := mp.ToOut(i); got < prev {
			t.Fatalf("ToOut not monotonic at %d: %d<%d", i, got, prev)
		} else {
			prev = got
		}
	}
	prev = -1
	for o := 0; o <= mp.OutLen(); o++ {
		got := mp.ToOrig(o)
		if got < prev {
			t.Fatalf("ToOrig not monotonic at %d", o)
		}
		prev = got
	}
	// "ab  \r\n": 空格(2,3)与 \r(4) 都映射到下一存活位置：输出 \n 的偏移 2
	wantToOut := map[int]int{2: 2, 3: 2, 4: 2}
	for i, want := range wantToOut {
		if got := mp.ToOut(i); got != want {
			t.Fatalf("deleted byte ToOut(%d)=%d want %d", i, got, want)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		cfg     norm.Config
		want    norm.Kind
		wantOff int
	}{
		{"nul", "ab\x00c", norm.Config{StrictNUL: true}, norm.ErrNUL, 2},
		{"ws limit", "a    x", norm.Config{WhitespaceLimit: 2}, norm.ErrWhitespaceLimit, 3},
		{"ws limit ok at eol", "a   \n", norm.Config{WhitespaceLimit: 3}, norm.ErrClosed, 0},
		{"out limit", "abc", norm.Config{OutputLimit: 2, Mode: norm.EnsureOne}, norm.ErrOutputLimit, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.want == norm.ErrClosed {
				n := norm.New(tc.cfg)
				if _, e := n.Write([]byte(tc.in)); e != nil {
					t.Fatal(e)
				}
				if e := n.Close(); e != nil {
					t.Fatal(e)
				}
				_, err = n.Write(nil)
			} else {
				_, _, err = norm.Normalize([]byte(tc.in), tc.cfg)
			}
			var ne norm.Error
			if !errors.As(err, &ne) || ne.Kind != tc.want {
				t.Fatalf("got %v want kind %d", err, tc.want)
			}
			if tc.want != norm.ErrClosed && ne.Offset != tc.wantOff {
				t.Fatalf("offset=%d want %d", ne.Offset, tc.wantOff)
			}
		})
	}
}

func TestIdempotent(t *testing.T) {
	for _, mode := range []norm.Trailing{norm.Keep, norm.EnsureOne, norm.Trim} {
		for seed, in := range []string{"", "\n", "\n\n", "  \r\n", "x\r\ny  \r\n\r", "\xff\x00 a \t\n"} {
			cfg := norm.Config{Mode: mode}
			out1, _, err := norm.Normalize([]byte(in), cfg)
			if err != nil {
				t.Fatal(err)
			}
			out2, _, err := norm.Normalize(out1, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out1, out2) {
				t.Fatalf("mode=%d seed=%d %q -> %q -> %q", mode, seed, in, out1, out2)
			}
			if bytes.ContainsAny(out1, "\r") {
				t.Fatalf("output contains CR: %q", out1)
			}
		}
	}
}
