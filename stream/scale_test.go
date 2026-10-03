package stream

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
	"unicode/utf8"
)

func TestUTF16(t *testing.T) {
	emoji := []byte{0xF0, 0x9F, 0x98, 0x80} // U+1F600
	cases := []struct {
		in   []byte
		cfg  Config
		want []byte
		err  error
	}{
		{[]byte{0xD8, 0x3D, 0xDE, 0x00}, Config{InUTF16: true}, emoji, nil},
		{[]byte{0x3D, 0xD8, 0x00, 0xDE}, Config{InUTF16: true, LE: true}, emoji, nil},
		{[]byte{0xD8, 0x00}, Config{InUTF16: true}, fffd, nil},
		{[]byte{0xDC, 0x00}, Config{InUTF16: true}, fffd, nil},
		{[]byte{0xD8, 0x00, 0x00, 0x41}, Config{InUTF16: true}, append(bytes.Clone(fffd), 0x41), nil},
		{[]byte{0xD8, 0x00, 0xD8, 0x00}, Config{InUTF16: true}, bytes.Repeat(fffd, 2), nil},
		{[]byte{0x00, 0x41, 0x00}, Config{InUTF16: true}, append([]byte{0x41}, fffd...), nil},
		{[]byte{0x00, 0x41, 0x00}, Config{InUTF16: true, Strict: true}, []byte{0x41}, ErrTruncated},
		{[]byte{0xD8, 0x00}, Config{InUTF16: true, Strict: true}, nil, ErrTruncated},
		{[]byte{0xDC, 0x00}, Config{InUTF16: true, Strict: true}, nil, ErrInvalid},
		{[]byte{0xFF, 0xFE, 0x41, 0x00}, Config{InUTF16: true}, []byte{0x41}, nil},
		{[]byte{0xFE, 0xFF, 0x00, 0x41}, Config{InUTF16: true, LE: true}, []byte{0x41}, nil},
		{[]byte{0x00, 0x41, 0xFE, 0xFF, 0x00, 0x42}, Config{InUTF16: true}, []byte{0x41, 0xEF, 0xBB, 0xBF, 0x42}, nil},
		{emoji, Config{OutUTF16: true}, []byte{0xD8, 0x3D, 0xDE, 0x00}, nil},
		{emoji, Config{OutUTF16: true, LE: true}, []byte{0x3D, 0xD8, 0x00, 0xDE}, nil},
	}
	for _, c := range cases {
		if got, err := run(c.cfg, c.in); !errors.Is(err, c.err) || !bytes.Equal(got, c.want) {
			t.Errorf("%X: got %X, %v", c.in, got, err)
		}
	}
	in := []byte{0xD8, 0x3D, 0xDE, 0x00, 0x00, 0x41}
	want, _ := run(Config{InUTF16: true}, in)
	for cut := 0; cut <= len(in); cut++ {
		if got, _ := run(Config{InUTF16: true}, in[:cut], in[cut:]); !bytes.Equal(got, want) {
			t.Fatalf("cut %d: got %X want %X", cut, got, want)
		}
	}
}

func TestRoundtripIdempotent(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 200; iter++ {
		in := []byte{0x61}
		for len(in) < 60 {
			r := rune(rng.Intn(0x110000))
			if !utf8.ValidRune(r) || r == 0xFEFF {
				continue
			}
			in = utf8.AppendRune(in, r)
		}
		u16out, _ := run(Config{OutUTF16: true, LE: iter%2 == 0}, in)
		if back, _ := run(Config{InUTF16: true, LE: iter%2 == 0}, u16out); !bytes.Equal(back, in) {
			t.Fatalf("roundtrip %X -> %X -> %X", in, u16out, back)
		}
	}
	for iter := 0; iter < 200; iter++ {
		in := make([]byte, 1+rng.Intn(60))
		rng.Read(in)
		o1, _ := run(Config{}, in)
		o2, _ := run(Config{}, o1)
		if !bytes.Equal(o1, o2) || !utf8.Valid(o1) {
			t.Fatalf("idempotent %X -> %X -> %X", in, o1, o2)
		}
	}
}

func TestConservationCap(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, cfg := range []Config{{}, {InUTF16: true}, {InUTF16: true, LE: true}} {
		for iter := 0; iter < 50; iter++ {
			in := make([]byte, 1+rng.Intn(200))
			rng.Read(in)
			tr := New(cfg)
			for _, b := range in {
				tr.Write([]byte{b})
				if len(tr.pend) > 3 {
					t.Fatalf("pend cap exceeded: %d", len(tr.pend))
				}
			}
			tr.Close()
			s := tr.Stats()
			if s.GoodBytes+s.BadBytes+s.BOMBytes != s.Consumed {
				t.Fatalf("conservation: %+v", s)
			}
		}
	}
}

func TestLimitResume(t *testing.T) {
	in := []byte("aé€😀bxyz")
	full, _ := run(Config{}, in)
	for limit := 4; limit <= len(full); limit++ {
		var got []byte
		rest := in
		for len(rest) > 0 {
			tr := New(Config{MaxOut: limit})
			n, err := tr.Write(rest)
			if err == nil {
				err = tr.Close()
			}
			got = append(got, tr.Output()...)
			if err == nil {
				break
			}
			if !errors.Is(err, ErrLimit) {
				t.Fatalf("limit %d: %v", limit, err)
			}
			rest = rest[n:]
		}
		if !bytes.Equal(got, full) {
			t.Fatalf("limit %d: got %X want %X", limit, got, full)
		}
	}
	tr := New(Config{OutUTF16: true, MaxOut: 6})
	tr.Write([]byte("😀😀"))
	if len(tr.Output()) != 4 {
		t.Fatalf("surrogate pair split: %X", tr.Output())
	}
}

func TestCheckCounts(t *testing.T) {
	for _, size := range []int{1 << 20, 1 << 24} {
		in := make([]byte, size)
		rand.New(rand.NewSource(3)).Read(in)
		for _, oneByte := range []bool{false, true} {
			tr := New(Config{})
			if oneByte {
				for _, b := range in {
					tr.Write([]byte{b})
				}
			} else {
				tr.Write(in)
			}
			tr.Close()
			if tr.checks > 2*int64(size) {
				t.Fatalf("size %d oneByte=%v: checks %d", size, oneByte, tr.checks)
			}
			t.Logf("size %d oneByte=%v checks %d", size, oneByte, tr.checks)
		}
	}
}
