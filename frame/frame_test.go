package frame

import (
	"errors"
	"io"
	"math/rand"
	"testing"

	"ontology/esc"
)

func naiveEncode(frames [][]byte) []byte {
	var out []byte
	for _, f := range frames {
		for _, b := range f {
			if b == '\\' || b == '\n' {
				out = append(out, '\\')
				if b == '\n' {
					b = 'n'
				}
			}
			out = append(out, b)
		}
		out = append(out, '\n')
	}
	return out
}
func randFrames(seed int64, n int) [][]byte {
	rng := rand.New(rand.NewSource(seed))
	fs := make([][]byte, n)
	for i := range fs {
		b := make([]byte, rng.Intn(14))
		for j := range b {
			b[j] = []byte{'\\', '\n', 'a', 'b', 'c', 'd'}[rng.Intn(6)]
		}
		fs[i] = b
	}
	return fs
}

func TestEscapeVectors(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""}, {"hi", "hi"}, {"a\nb", "a\\nb"},
		{"x\\y", "x\\\\y"}, {"\n\\", "\\n\\\\"},
	} {
		if got := string(esc.Escape([]byte(c.in))); got != c.want {
			t.Errorf("Escape(%q)=%q want %q", c.in, got, c.want)
		}
		if got, err := esc.Unescape([]byte(c.want)); err != nil || string(got) != c.in {
			t.Errorf("Unescape(%q)=%q,%v want %q", c.want, got, err, c.in)
		}
	}
	for _, c := range []struct {
		in   string
		want error
	}{
		{"a\\x", esc.ErrIllegalEscape}, {"\\\n", esc.ErrDanglingEscape},
		{"abc\\", esc.ErrDanglingEscape}, {"\\", esc.ErrDanglingEscape},
	} {
		if got, err := esc.Unescape([]byte(c.in)); !errors.Is(err, c.want) || got != nil {
			t.Errorf("Unescape(%q)=%v,%v want %v", c.in, got, err, c.want)
		}
	}
}
func TestRoundTrip(t *testing.T) {
	cases := [][][]byte{{}, {[]byte("")}, {{}, []byte("x"), {}}, {[]byte("hi"), []byte("a\nb"), []byte("x\\y")}}
	for r := 0; r < 40; r++ {
		cases = append(cases, randFrames(int64(r), 1+r%8))
	}
	for idx, fs := range cases {
		got, err := Decode(Encode(fs))
		if err != nil || len(got) != len(fs) {
			t.Fatalf("case %d: %v %v", idx, got, err)
		}
		for i := range fs {
			if string(got[i]) != string(fs[i]) {
				t.Errorf("case %d frame %d: %q != %q", idx, i, got[i], fs[i])
			}
		}
	}
}

func TestEncodeMatchesNaive(t *testing.T) {
	cases := [][][]byte{{}, {{}, []byte("a\nb"), []byte("x\\y")}}
	for r := 0; r < 40; r++ {
		cases = append(cases, randFrames(int64(100+r), r%10))
	}
	for idx, fs := range cases {
		if got, want := Encode(fs), naiveEncode(fs); string(got) != string(want) {
			t.Fatalf("case %d:\n got % x\nwant % x", idx, got, want)
		}
	}
}

func TestDecodeFailures(t *testing.T) {
	if esc.ErrIllegalEscape == esc.ErrDanglingEscape || esc.ErrDanglingEscape == ErrMissingTerminator ||
		esc.ErrIllegalEscape == ErrMissingTerminator {
		t.Fatal("the three sentinels must be distinct")
	}
	for _, c := range []struct {
		name, raw string
		want      error
	}{
		{"illegal x", "a\\x\n", esc.ErrIllegalEscape},
		{"illegal k", "\\k\n", esc.ErrIllegalEscape},
		{"dangling at delimiter", "ab\\\n", esc.ErrDanglingEscape},
		{"dangling at eof", "ab\\", esc.ErrDanglingEscape},
		{"missing terminator", "ab", ErrMissingTerminator},
		{"after frame", "ok\nab", ErrMissingTerminator},
	} {
		got, err := Decode([]byte(c.raw))
		if !errors.Is(err, c.want) || got != nil {
			t.Errorf("%s: frames=%v err=%v want %v nil-frames", c.name, got, err, c.want)
		}
	}
}

func TestReaderCursorNoAdvance(t *testing.T) {
	r := NewReader([]byte("ok\na\\x\nz\n"))
	f, err := r.NextFrame()
	if err != nil || string(f) != "ok" {
		t.Fatalf("first frame: %q %v", f, err)
	}
	p := r.Pos()
	for i := 0; i < 3; i++ {
		if _, err := r.NextFrame(); !errors.Is(err, esc.ErrIllegalEscape) || r.Pos() != p {
			t.Fatalf("try %d: cursor=%d err=%v", i, r.Pos(), err)
		}
	}
	r2 := NewReader(Encode([][]byte{{'a'}, []byte("b\nb"), {}}))
	for _, w := range []string{"a", "b\nb", ""} {
		if f, err := r2.NextFrame(); err != nil || string(f) != w {
			t.Fatalf("frame %q: %q %v", w, f, err)
		}
	}
	if _, err := r2.NextFrame(); err != io.EOF {
		t.Fatalf("end: %v want EOF", err)
	}
}

func TestSinglePassCounter(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		stream := Encode(randFrames(int64(m), m))
		got, err := Decode(stream)
		ex, rr := lastDecode.examined.Load(), lastDecode.reread.Load()
		if err != nil || len(got) != m || ex != int64(len(stream)) || rr != 0 {
			t.Fatalf("m=%d err=%v n=%d examined=%d/%d reread=%d", m, err, len(got), ex, len(stream), rr)
		}
	}
}
