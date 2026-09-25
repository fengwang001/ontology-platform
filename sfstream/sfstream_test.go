package sfstream

import (
	"bytes"
	"sync"
	"testing"

	"ontology/sfano"
)

var demoFreq = map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}

func build(t *testing.T, freq map[byte]int) (*Codec, map[byte]string) {
	t.Helper()
	codes, err := sfano.Build(freq)
	if err != nil {
		t.Fatal(err)
	}
	return New(codes), codes
}

// TestNodeCountBound decodes single symbols for growing alphabets and
// asserts the prefix-tree nodes visited per symbol stay bounded by the
// max code length instead of growing linearly with m. (byte alphabet
// caps m at 256; decreasing frequencies keep depth ~log m.)
func TestNodeCountBound(t *testing.T) {
	for _, m := range []int{100, 200, 256} {
		freq := map[byte]int{}
		for i := 0; i < m; i++ {
			freq[byte(i)] = m - i
		}
		c, codes := build(t, freq)
		maxLen := 0
		for _, cd := range codes {
			if len(cd) > maxLen {
				maxLen = len(cd)
			}
		}
		if maxLen >= m/4 { // depth must stay far below alphabet size
			t.Fatalf("m=%d: max code length %d grows with m", m, maxLen)
		}
		for _, s := range []byte{0, byte(m / 2), byte(m - 1)} {
			enc, err := c.Encode([]byte{s})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Decode(enc, 1); err != nil {
				t.Fatal(err)
			}
			if got := c.last.Load(); got > int64(maxLen) {
				t.Errorf("m=%d sym=%d: visited %d nodes > maxLen %d", m, s, got, maxLen)
			}
		}
	}
}

// TestFeedChunkAgnostic feeds the same stream in every possible chunking
// and requires the decoded symbols to equal the one-shot Decode result.
func TestFeedChunkAgnostic(t *testing.T) {
	c, _ := build(t, demoFreq)
	msg := []byte("AABCCDEEDCBAED")
	enc, err := c.Encode(msg)
	if err != nil {
		t.Fatal(err)
	}
	want, err := c.Decode(enc, len(msg))
	if err != nil {
		t.Fatal(err)
	}
	for size := 1; size <= len(enc); size++ {
		f := c.NewFeeder()
		for i := 0; i < len(enc); i += size {
			j := i + size
			if j > len(enc) {
				j = len(enc)
			}
			f.Feed(enc[i:j])
		}
		got, err := f.Decode(len(msg))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("chunk size %d: got %q err %v, want %q", size, got, err, want)
		}
	}
}

// TestDeterministicPrefixFree rebuilds tables repeatedly and checks no
// codeword prefixes another.
func TestDeterministicPrefixFree(t *testing.T) {
	freqs := []map[byte]int{demoFreq, {'A': 6, 'B': 4, 'C': 3, 'D': 1}, {'x': 7, 'y': 7, 'z': 7}}
	for _, freq := range freqs {
		base, err := sfano.Build(freq)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			again, _ := sfano.Build(freq)
			for s, cd := range base {
				if again[s] != cd {
					t.Fatalf("nondeterministic code for %q", s)
				}
			}
		}
		for s1, c1 := range base {
			for s2, c2 := range base {
				if s1 != s2 && len(c2) > len(c1) && c2[:len(c1)] == c1 {
					t.Errorf("%q prefixes %q", c1, c2)
				}
			}
		}
	}
}

// TestConcurrentRoundtrip hammers one codec from many goroutines; every
// result must match the serial result byte for byte.
func TestConcurrentRoundtrip(t *testing.T) {
	c, _ := build(t, demoFreq)
	msg := []byte("AABCCDEEDCBA")
	senc, _ := c.Encode(msg)
	sdec, _ := c.Decode(senc, len(msg))
	var wg sync.WaitGroup
	res := make(chan bool, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, err1 := c.Encode(msg)
			d, err2 := c.Decode(e, len(msg))
			res <- err1 == nil && err2 == nil && bytes.Equal(e, senc) && bytes.Equal(d, sdec)
		}()
	}
	wg.Wait()
	for i := 0; i < 32; i++ {
		if !<-res {
			t.Error("concurrent result differs from serial")
		}
	}
}
