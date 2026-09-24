package stream_test

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
	"ontology/stream"
	"ontology/u16"
	"ontology/u8"
)

func hx(s ...byte) []byte { return s }

func runAll(t *testing.T, c stream.Config, in []byte, chunk int) ([]byte, stream.Stats, error) {
	t.Helper()
	tr := stream.New(c)
	for i := 0; i < len(in); i += chunk {
		end := i + chunk
		if end > len(in) { end = len(in) }
		if _, err := tr.Write(in[i:end]); err != nil { return tr.Output(), tr.Stats(), err }
	}
	return tr.Output(), tr.Stats(), tr.Close()
}

func TestReplacementSamples(t *testing.T) {
	cases := []struct{ in, want []byte }{
		{hx(0xf0, 0x90, 0x80, 0x41), hx(0xef, 0xbf, 0xbd, 0x41)},
		{hx(0xe0, 0x80, 0x80), bytes.Repeat([]byte{0xef, 0xbf, 0xbd}, 3)},
		{hx(0xed, 0xa0, 0x80), bytes.Repeat([]byte{0xef, 0xbf, 0xbd}, 3)},
		{hx(0xc0, 0xaf), bytes.Repeat([]byte{0xef, 0xbf, 0xbd}, 2)},
		{hx(0xf4, 0x90, 0x80, 0x80), bytes.Repeat([]byte{0xef, 0xbf, 0xbd}, 4)},
		{hx(0xe2, 0x82), []byte{0xef, 0xbf, 0xbd}},
		{hx(0x80, 0x80), bytes.Repeat([]byte{0xef, 0xbf, 0xbd}, 2)},
	}
	for _, tc := range cases {
		c := stream.Config{Direction: stream.UTF16LEToUTF8}
		got, _, err := runAll(t, c, tc.in, 1)
		if err != nil || !bytes.Equal(got, tc.want) { t.Fatalf("%x: got %x err %v", tc.in, got, err) }
	}
}

func TestStrictOffsetAndTerminal(t *testing.T) {
	in := hx(0x41, 0xf0, 0x90, 0x80, 0x41)
	tr := stream.New(stream.Config{Direction: stream.UTF16LEToUTF8, Strict: true})
	_, err := tr.Write(in)
	var se *stream.Error
	if !errors.As(err, &se) || !errors.Is(se, stream.ErrIllegal) || se.Offset != 1 || se.Length != 3 { t.Fatal(err) }
	_, err2 := tr.Write(in)
	if !errors.Is(err2, stream.ErrIllegal) { t.Fatal(err2) }
}

func TestAllSplitsMatch(t *testing.T) {
	inputs := [][]byte{
		append([]byte("αβγ"), hx(0xf0,0x90,0x80,0x41,0xe0,0x80,0x80,0xef,0xbb,0xbf,0xc3)...),
		bytes.Repeat([]byte{0x80}, 8),
		{0xef, 0xbb, 0xbf, 'A', 0xef, 0xbb, 0xbf},
	}
	cfg := stream.Config{Direction: stream.UTF16LEToUTF8}
	for _, in := range inputs {
		base, _, _ := runAll(t, cfg, in, len(in)+1)
		for cut := 1; cut <= len(in); cut++ {
			got, _, err := runAll(t, cfg, in, cut)
			if err != nil || !bytes.Equal(got, base) { t.Fatalf("cut %d: %x != %x", cut, got, base) }
		}
	}
}

func TestTruncationErrors(t *testing.T) {
	in := []byte("a界🙂")
	for i := 0; i <= len(in); i++ {
		tr := stream.New(stream.Config{Direction: stream.UTF16LEToUTF8, Strict: true})
		_, _ = tr.Write(in[:i])
		err := tr.Close()
		boundaries := map[int]bool{0: true, 1: true, 4: true, len(in): true}
		wantTrunc := !boundaries[i]
		if wantTrunc != errors.Is(err, stream.ErrTruncated) { t.Fatal(i, err) }
	}
}

func TestUTF16SurrogatesAndBOM(t *testing.T) {
	r := []rune{0xd800, 'A', 0xdc00}
	in := make([]byte, 0, 6)
	for _, q := range r { in = append(in, u16.CodeBytes(q, true)...) }
	got, _, err := runAll(t, stream.Config{Direction: stream.UTF16LEToUTF8}, in, 3)
	if err != nil || !bytes.Equal(got, append([]byte{0xef,0xbf,0xbd,'A'}, 0xef,0xbf,0xbd)) { t.Fatalf("%x %v", got, err) }
	bom := append(u16.CodeBytes(0xfeff, true), u16.CodeBytes(0xfeff, true)...)
	out, _, _ := runAll(t, stream.Config{Direction: stream.UTF16LEToUTF8, KeepBOM: true}, bom, 1)
	if !bytes.Equal(out, bytes.Repeat([]byte{0xef,0xbb,0xbf}, 2)) { t.Fatalf("%x", out) }
}

func TestRoundTripIdempotenceConservationLimit(t *testing.T) {
	rng := rand.New(rand.NewSource(1)); in := []byte("abcα🙂")
	for i := 0; i < 100; i++ { in = append(in, byte(rng.Intn(256))) }
	cfg := stream.Config{Direction: stream.UTF16LEToUTF8}
	out, s1, err := runAll(t, cfg, in, 1)
	if err != nil { t.Fatal(err) }
	again, _, _ := runAll(t, cfg, out, 1)
	if !bytes.Equal(out, again) { t.Fatal("not idempotent") }
	if s1.Scalars+s1.IllegalBytes+s1.BOMBytes != s1.ConsumedBytes { t.Fatal(s1) }
	tr := stream.New(stream.Config{Direction: stream.UTF16LEToUTF8, OutputLimit: 10})
	n, err := tr.Write(in)
	if !errors.Is(err, stream.ErrLimit) || n == 0 { t.Fatal(n, err) }
	rest := in[n:]
	tr2 := stream.New(stream.Config{Direction: stream.UTF16LEToUTF8, OutputLimit: 0})
	_, _ = tr2.Write(rest); _ = tr2.Close()
	full, _, _ := runAll(t, cfg, in, len(in)+1)
	if !bytes.Equal(append(tr.Output(), tr2.Output()...), full) { t.Fatal("resume mismatch") }
}

func TestUTF16RoundTrip(t *testing.T) {
	in := []byte("abc界🙂")
	to16 := stream.New(stream.Config{Direction: stream.UTF8ToUTF16LE})
	_, _ = to16.Write(in); _ = to16.Close()
	back := stream.New(stream.Config{Direction: stream.UTF16LEToUTF8})
	_, _ = back.Write(to16.Output()); _ = back.Close()
	if !bytes.Equal(back.Output(), in) { t.Fatalf("%x", back.Output()) }
}

func TestCheckBudget(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, size := range []int{1 << 20, 16 << 20} {
		in := make([]byte, size)
		for i := range in { b := byte(rng.Intn(256)); if rng.Intn(10) == 0 { b = byte(128 + rng.Intn(128)) }; in[i] = b }
		cfg := stream.Config{Direction: stream.UTF16LEToUTF8}
		for chunk, factor := range map[int]int64{size: 2, 1: 2} {
			_, s, err := runAll(t, cfg, in, chunk)
			if err != nil || s.BytesChecked > factor*int64(size) { t.Fatalf("size %d chunk %d checks %d err %v", size, chunk, s.BytesChecked, err) }
		}
	}
}

var _ = u8.Next
