package lzw

import (
	"bytes"
	"math/rand"
	"testing"
)

func newRand(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }

// TestRandomVsNaive compares the streaming encoder byte-for-byte against the
// step-by-step naive transcription over random inputs spanning several table
// resets and all four widths, and verifies the decoder restores every byte.
func TestRandomVsNaive(t *testing.T) {
	patterns := []func(*rand.Rand, int) []byte{
		func(r *rand.Rand, n int) []byte {
			b := make([]byte, n)
			r.Read(b)
			return b
		},
		func(r *rand.Rand, n int) []byte {
			// low-entropy: long runs of a small alphabet, grows the table.
			b := make([]byte, n)
			alpha := []byte{0, 1, 2, 3, 'A', 'B'}
			prev := 0
			for i := range b {
				if r.Intn(3) == 0 {
					prev = r.Intn(len(alpha))
				} else {
					// usually stay on the same symbol, occasionally drift
					if r.Intn(8) == 0 {
						prev = r.Intn(len(alpha))
					}
				}
				b[i] = alpha[prev]
			}
			return b
		},
		func(r *rand.Rand, n int) []byte {
			return bytes.Repeat([]byte{byte(r.Intn(256))}, n)
		},
	}
	total := 0
	for seed := int64(0); seed < 120; seed++ {
		r := newRand(seed)
		n := r.Intn(20000)
		in := patterns[int(seed)%len(patterns)](r, n)
		total += n
		_, packed, _ := naiveEncode(in)
		encSplit := 1 + r.Intn(64)
		got := encodeChunked(t, in, encSplit)
		if !bytes.Equal(got, packed) {
			t.Fatalf("seed=%d n=%d split=%d: encoder differs from naive (encLen=%d naiveLen=%d)",
				seed, n, encSplit, len(got), len(packed))
		}
		decSplit := 1 + r.Intn(64)
		dec := decodeChunked(t, got, decSplit)
		if !bytes.Equal(dec, in) {
			t.Fatalf("seed=%d n=%d: decode mismatch", seed, n)
		}
	}
	t.Logf("compared %d random input bytes against naive reference", total)
}

// TestReplayDeterminism replays identical inputs and asserts byte-identical
// code streams.
func TestReplayDeterminism(t *testing.T) {
	in := make([]byte, 5000)
	newRand(7).Read(in)
	first := encodeChunked(t, in, 1)
	for i := 0; i < 5; i++ {
		if got := encodeChunked(t, in, 1+i); !bytes.Equal(got, first) {
			t.Fatalf("replay %d differs", i)
		}
	}
}
