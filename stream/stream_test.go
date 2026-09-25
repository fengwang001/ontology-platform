package stream_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/stream"
)

func TestReplacementSamples(t *testing.T) {
	cases := []struct {
		in, want []byte
	}{
		{[]byte{0xF0, 0x90, 0x80, 0x41}, []byte("\ufffdA")},
		{[]byte{0xE0, 0x80, 0x80}, []byte("\ufffd\ufffd\ufffd")},
		{[]byte{0xED, 0xA0, 0x80}, []byte("\ufffd\ufffd\ufffd")},
		{[]byte{0xC0, 0xAF}, []byte("\ufffd\ufffd")},
		{[]byte{0xF4, 0x90, 0x80, 0x80}, []byte("\ufffd\ufffd\ufffd\ufffd")},
		{[]byte{0xE2, 0x82}, []byte("\ufffd")},
		{[]byte{0x80, 0x80}, []byte("\ufffd\ufffd")},
	}
	for _, c := range cases {
		splitAll(t, stream.U8toU8, c.in, stream.Config{})
		if got, _, _ := runAll(stream.U8toU8, c.in, stream.Config{}); string(got) != string(c.want) {
			t.Fatalf("in %x want %x got %x", c.in, c.want, got)
		}
	}
}

func TestStrictOffset(t *testing.T) {
	samples := []struct {
		in      []byte
		off, ln int
	}{
		{[]byte{0x41, 0xF0, 0x90, 0x80, 0x41}, 1, 3},
		{[]byte{0xE0, 0x80, 0x80}, 0, 1},
		{[]byte{0xED, 0xA0, 0x80}, 0, 1},
		{[]byte{0x41, 0x42, 0x80, 0x43}, 2, 1},
		{[]byte{0xF4, 0x90, 0x80, 0x80}, 0, 1},
	}
	for _, s := range samples {
		for cut := 1; cut <= len(s.in); cut++ {
			tr := stream.New(stream.Config{Dir: stream.U8toU8, Strict: true})
			var err error
			for i := 0; i < len(s.in); i += cut {
				j := min(i+cut, len(s.in))
				if _, e := tr.Write(s.in[i:j]); e != nil {
					err = e
					break
				}
			}
			var ie *stream.InvalidError
			if !errors.As(err, &ie) || ie.Offset != s.off || ie.Length != s.ln {
				t.Fatalf("in %x cut %d want off=%d len=%d got %#v", s.in, cut, s.off, s.ln, err)
			}
			saved := append([]byte(nil), tr.Output()...)
			if _, e := tr.Write([]byte{0x41}); !errors.Is(e, stream.ErrInvalid) {
				t.Fatalf("terminal write %v", e)
			}
			if string(saved) != string(tr.Output()) {
				t.Fatal("output mutated after terminal error")
			}
		}
	}
}

func TestTruncVsInvalid(t *testing.T) {
	base := []byte{'a', 0xC3, 0xA9, 0xE4, 0xB8, 0xAD, 0xF0, 0x9F, 0x98, 0x80}
	bounds := map[int]bool{0: true, 1: true, 3: true, 6: true, 10: true}
	for n := 0; n <= len(base); n++ {
		tr := stream.New(stream.Config{Dir: stream.U8toU8, Strict: true})
		if _, err := tr.Write(base[:n]); err != nil {
			t.Fatal(err)
		}
		err := tr.Close()
		if boundaries[n] && err != nil {
			t.Fatalf("n=%d boundary: %v", n, err)
		}
		if !boundaries[n] && !errors.Is(err, stream.ErrTruncated) {
			t.Fatalf("n=%d want truncated got %v", n, err)
		}
	}
	tr := stream.New(stream.Config{Dir: stream.U8toU8, Strict: true})
	tr.Write([]byte{0x80})
	if !errors.Is(tr.Close(), stream.ErrInvalid) {
		t.Fatal("stray cont must be invalid, not truncated")
	}
}

func TestConservation(t *testing.T) {
	rng := rand.New(rand.NewState(rand.NewSource(1)))
	_ = rng
	for iter := 0; iter < 100; iter++ {
		in := make([]byte, rng.Intn(300))
		rng.Read(in)
		for cut := 1; cut <= 7; cut++ {
			tr := stream.New(stream.Config{Dir: stream.U8toU8})
			cons := 0
			for i := 0; i < len(in); i += cut {
				n, err := tr.Write(in[i:min(i+cut, len(in))])
				cons += n
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := tr.Close(); err != nil {
				t.Fatal(err)
			}
			st := tr.Stats()
			if st.LegalBytes+st.BadBytes+st.BOMBytes != len(in) || st.Consumed != len(in) || cons != len(in) {
				t.Fatalf("conservation %#v cons=%d n=%d", st, cons, len(in))
			}
		}
	}
}
