package api

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"ontology/hpack"
)

var seqs = map[string][]int{
	"empty":   {},
	"single":  {11},
	"mixed":   {1, 2, 4, 8, 15, 9, 3, 11},
	"allvals": {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
}

// TestAgainstNaive: Encode output matches the naive reference byte for
// byte; Decode of any single-bit-corrupted stream matches the source.
func TestAgainstNaive(t *testing.T) {
	for name, ns := range seqs {
		enc, err := Encode(ns)
		if err != nil || !bytes.Equal(enc, naiveEncode(ns)) {
			t.Fatalf("%s: encode != naive", name)
		}
		for bit := 0; bit < 7*len(ns); bit++ {
			c := bytes.Clone(enc)
			c[bit/8] ^= 1 << (7 - bit%8)
			got, err := Decode(c, len(ns))
			if err != nil || !equal(got, ns) {
				t.Fatalf("%s: single error bit %d not corrected", name, bit)
			}
		}
	}
}

// TestChunkedFeed: any chunking of the stream decodes to the same nibbles.
func TestChunkedFeed(t *testing.T) {
	for name, ns := range seqs {
		enc, _ := Encode(ns)
		for size := 1; size <= len(enc)+1; size++ {
			var f hpack.Feeder
			for i := 0; i < len(enc); i += size {
				f.Feed(enc[i:min(i+size, len(enc))])
			}
			got, err := Decode(f.Bytes(), len(ns))
			if err != nil || !equal(got, ns) {
				t.Fatalf("%s chunk=%d: mismatch", name, size)
			}
		}
	}
}

// TestDeterministic: repeated encodes are byte-identical.
func TestDeterministic(t *testing.T) {
	for name, ns := range seqs {
		a, _ := Encode(ns)
		for i := 0; i < 5; i++ {
			b, _ := Encode(ns)
			if !bytes.Equal(a, b) {
				t.Fatalf("%s: nondeterministic encode", name)
			}
		}
	}
}

// TestSingleErrorCorrection: flipping any one bit still decodes correctly.
func TestSingleErrorCorrection(t *testing.T) {
	for n := 0; n < 16; n++ {
		enc, _ := Encode([]int{n})
		for pos := 0; pos < 7; pos++ {
			c := bytes.Clone(enc)
			c[pos/8] ^= 1 << (7 - pos%8)
			got, err := Decode(c, 1)
			if err != nil || got[0] != n {
				t.Fatalf("n=%d pos=%d: got %v err %v", n, pos, got, err)
			}
		}
	}
}

// TestErrorPaths: the three failures are distinct sentinels, return nil,
// and leave the (stateless) API fully usable afterwards.
func TestErrorPaths(t *testing.T) {
	cases := []struct {
		name string
		err  error
		run  func() ([]int, error)
	}{
		{"nibble>15", ErrInvalidNibble, func() ([]int, error) { _, e := Encode([]int{16}); return nil, e }},
		{"nibble<0", ErrInvalidNibble, func() ([]int, error) { _, e := Encode([]int{-1}); return nil, e }},
		{"truncated", ErrTruncated, func() ([]int, error) { return Decode([]byte{0xff}, 2) }},
		{"padding", ErrIllegalPadding, func() ([]int, error) { return Decode([]byte{0x67}, 1) }},
	}
	for _, c := range cases {
		got, err := c.run()
		if !errors.Is(err, c.err) || got != nil {
			t.Fatalf("%s: got %v err %v", c.name, got, err)
		}
		for _, other := range []error{ErrInvalidNibble, ErrTruncated, ErrIllegalPadding} {
			if other != c.err && errors.Is(err, other) {
				t.Fatalf("%s: collides with %v", c.name, other)
			}
		}
	}
	if got, err := Decode(mustEnc(t, []int{5, 6}), 2); err != nil || !equal(got, []int{5, 6}) {
		t.Fatalf("API unusable after rejections: %v %v", got, err)
	}
}

// TestConcurrentDecode: N goroutines match the serial result. No sleeps.
func TestConcurrentDecode(t *testing.T) {
	ns := []int{11, 0, 15, 7, 8, 3, 14, 2}
	enc := mustEnc(t, ns)
	enc[3] ^= 0x20 // inject one bit error
	want, err := Decode(enc, len(ns))
	if err != nil {
		t.Fatal(err)
	}
	const n = 64
	results := make([][]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := Decode(enc, len(ns))
			if err != nil {
				t.Error(err)
			}
			results[i] = got
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if !equal(results[i], want) {
			t.Fatalf("goroutine %d mismatch", i)
		}
	}
}

func mustEnc(t *testing.T, ns []int) []byte {
	t.Helper()
	b, err := Encode(ns)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
