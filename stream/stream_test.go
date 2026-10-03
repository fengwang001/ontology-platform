package stream

import (
	"bytes"
	"errors"
	"testing"
)

var fffd = []byte{0xEF, 0xBF, 0xBD}

func run(cfg Config, chunks ...[]byte) ([]byte, error) {
	tr := New(cfg)
	for _, c := range chunks {
		if _, err := tr.Write(c); err != nil {
			return tr.Output(), err
		}
	}
	err := tr.Close()
	return tr.Output(), err
}

func TestReplaceSamples(t *testing.T) {
	cases := []struct{ in, want []byte }{
		{[]byte{0xF0, 0x90, 0x80, 0x41}, append(bytes.Clone(fffd), 0x41)},
		{[]byte{0xE0, 0x80, 0x80}, bytes.Repeat(fffd, 3)},
		{[]byte{0xED, 0xA0, 0x80}, bytes.Repeat(fffd, 3)},
		{[]byte{0xC0, 0xAF}, bytes.Repeat(fffd, 2)},
		{[]byte{0xF4, 0x90, 0x80, 0x80}, bytes.Repeat(fffd, 4)},
		{[]byte{0xE2, 0x82}, fffd},
		{[]byte{0x80, 0x80}, bytes.Repeat(fffd, 2)},
	}
	for _, c := range cases {
		if got, err := run(Config{}, c.in); err != nil || !bytes.Equal(got, c.want) {
			t.Errorf("%X: got %X, %v", c.in, got, err)
		}
	}
}

func TestStrictOffsetLen(t *testing.T) {
	cases := []struct {
		in  [][]byte
		off int64
		n   int
	}{
		{[][]byte{{0x61, 0xF0, 0x90, 0x80, 0x41}}, 1, 3},
		{[][]byte{{0xE0, 0x80, 0x80}}, 0, 1},
		{[][]byte{{0xC0, 0xAF}}, 0, 1},
		{[][]byte{{0x61, 0x62, 0x80}}, 2, 1},
		{[][]byte{{0xF4, 0x90, 0x80, 0x80}}, 0, 1},
		{[][]byte{{0xE2, 0x82}, {0x41}}, 0, 2},
	}
	for _, c := range cases {
		tr := New(Config{Strict: true})
		var err error
		for _, ch := range c.in {
			if _, err = tr.Write(ch); err != nil {
				break
			}
		}
		var le *Error
		if !errors.As(err, &le) || le.Off != c.off || le.Len != c.n || !errors.Is(err, ErrInvalid) {
			t.Errorf("%X: got %v", c.in, err)
		}
		if _, err2 := tr.Write([]byte{0x61}); !errors.Is(err2, ErrInvalid) {
			t.Errorf("%X: terminal write got %v", c.in, err2)
		}
	}
}

func TestSplitConsistency(t *testing.T) {
	inputs := [][]byte{
		[]byte("aé€😀b"),
		{0xF0, 0x90, 0x80, 0x41, 0xE0, 0x80, 0x80},
		{0xEF, 0xBB, 0xBF, 0x61, 0xEF, 0xBB, 0xBF},
		{0xED, 0xA0, 0x80, 0xC0, 0xAF, 0xE2, 0x82},
	}
	for _, in := range inputs {
		want, _ := run(Config{}, in)
		for cut := 0; cut <= len(in); cut++ {
			if got, _ := run(Config{}, in[:cut], in[cut:]); !bytes.Equal(got, want) {
				t.Fatalf("%X cut %d: got %X want %X", in, cut, got, want)
			}
		}
		tr := New(Config{})
		for _, b := range in {
			tr.Write([]byte{b})
		}
		tr.Close()
		if !bytes.Equal(tr.Output(), want) {
			t.Fatalf("%X 1-byte: got %X", in, tr.Output())
		}
	}
	in := []byte{0x61, 0xF0, 0x90, 0x80, 0x41, 0x62}
	for cut := 0; cut <= len(in); cut++ {
		tr := New(Config{Strict: true})
		tr.Write(in[:cut])
		_, err := tr.Write(in[cut:])
		var le *Error
		if !errors.As(err, &le) || le.Off != 1 || le.Len != 3 {
			t.Fatalf("cut %d: %v", cut, err)
		}
	}
}

func TestTruncationInjection(t *testing.T) {
	in := []byte("aé€😀b")
	for cut := 0; cut <= len(in); cut++ {
		tr := New(Config{Strict: true})
		tr.Write(in[:cut])
		err := tr.Close()
		if mid := cut < len(in) && in[cut]&0xC0 == 0x80; mid {
			if !errors.Is(err, ErrTruncated) || errors.Is(err, ErrInvalid) {
				t.Errorf("cut %d: want ErrTruncated, got %v", cut, err)
			}
		} else if err != nil {
			t.Errorf("cut %d: want nil, got %v", cut, err)
		}
	}
}

func TestBOM(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	cases := []struct {
		in   [][]byte
		cfg  Config
		want []byte
	}{
		{[][]byte{append(bytes.Clone(bom), 0x61)}, Config{}, []byte{0x61}},
		{[][]byte{append(bytes.Clone(bom), 0x61)}, Config{KeepBOM: true}, append(bytes.Clone(bom), 0x61)},
		{[][]byte{{0xEF, 0xBB}, {0xBF, 0x61}}, Config{}, []byte{0x61}},
		{[][]byte{{0x61, 0xEF, 0xBB, 0xBF, 0x62}}, Config{}, []byte{0x61, 0xEF, 0xBB, 0xBF, 0x62}},
		{[][]byte{bom}, Config{OutUTF16: true}, nil},
		{[][]byte{append(bytes.Clone(bom), 0x61)}, Config{OutUTF16: true, KeepBOM: true}, []byte{0xFE, 0xFF, 0x00, 0x61}},
	}
	for _, c := range cases {
		if got, err := run(c.cfg, c.in...); err != nil || !bytes.Equal(got, c.want) {
			t.Errorf("%X: got %X, %v", c.in, got, err)
		}
	}
}
