package lz77_test

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"

	"ontology/internal/dec"
	"ontology/internal/enc"
)

func compress(t *testing.T, p []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w, err := enc.NewWriter(&b, enc.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(p); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decompress(t *testing.T, z, chunk []byte, limit uint64) []byte {
	t.Helper()
	r := dec.NewReader(limit)
	if len(chunk) == 0 {
		if _, err := r.Write(z); err != nil {
			t.Fatal(err)
		}
	} else {
		for i := 0; i < len(z); i += len(chunk) {
			end := i + len(chunk)
			if end > len(z) {
				end = len(z)
			}
			if _, err := r.Write(z[i:end]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return r.Output()
}

func TestRoundTripAndOverlap(t *testing.T) {
	q := make([]byte, 10000)
	rand.New(rand.NewSource(1)).Read(q)
	cases := map[string][]byte{
		"empty":   nil,
		"same":    bytes.Repeat([]byte{'x'}, 5000),
		"random":  q,
		"period1": bytes.Repeat([]byte{'a'}, 1000),
		"period2": bytes.Repeat([]byte{'a', 'b'}, 1000),
		"period3": bytes.Repeat([]byte{'a', 'b', 'c'}, 1000),
	}
	for _, n := range []int{1, 2, 3, 13, 64} {
		cases["overlap"] = bytes.Repeat([]byte("abcdefgh")[:n], 70)
		z := compress(t, cases["overlap"])
		if !bytes.Equal(decompress(t, z, nil, 0), cases["overlap"]) {
			t.Fatalf("overlap distance %d failed", n)
		}
	}
	for _, in := range cases {
		if got := decompress(t, compress(t, in), nil, 0); !bytes.Equal(got, in) {
			t.Fatal("round trip mismatch")
		}
	}
}

func TestWriterChunksAndFlush(t *testing.T) {
	in := bytes.Repeat([]byte("abcdefghij"), 500)
	var outs [][]byte
	for _, n := range []int{1, 7, len(in)} {
		var b bytes.Buffer
		w, _ := enc.NewWriter(&b, enc.DefaultConfig())
		for j := 0; j < len(in); j += n {
			end := j + n
			if end > len(in) {
				end = len(in)
			}
			if _, err := w.Write(in[j:end]); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		outs = append(outs, b.Bytes())
	}
	if !bytes.Equal(outs[0], outs[1]) || !bytes.Equal(outs[1], outs[2]) {
		t.Fatal("chunking changed output")
	}
	var b bytes.Buffer
	w, _ := enc.NewWriter(&b, enc.DefaultConfig())
	_, _ = w.Write(in[:200])
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	first := b.Len()
	if err := w.Flush(); err != nil || b.Len() != first {
		t.Fatal("empty flush emitted bytes")
	}
	_, _ = w.Write(in[200:])
	if err := w.Close(); err != nil || !bytes.Equal(decompress(t, b.Bytes(), nil, 0), in) {
		t.Fatal("flush round trip failed")
	}
}

func TestDecoderChunksAndParallel(t *testing.T) {
	in := bytes.Repeat([]byte("abcdefghijklmnop"), 2000)
	z := compress(t, in)
	for _, n := range []int{1, 3, 7, 128, 1 << 20} {
		got := decompress(t, z, make([]byte, n), 0)
		if !bytes.Equal(got, in) {
			t.Fatalf("chunk %d mismatch", n)
		}
	}
	var ref []byte
	for _, workers := range []int{1, 2, 4, 8} {
		out, err := enc.CompressParallel(in, 333, workers, enc.DefaultConfig())
		if err != nil || !bytes.Equal(decompress(t, out, make([]byte, 5), 0), in) {
			t.Fatalf("workers %d failed", workers)
		}
		if ref == nil {
			ref = out
		} else if !bytes.Equal(ref, out) {
			t.Fatalf("workers %d changed output", workers)
		}
	}
}

func TestParallelRepeatedRace(t *testing.T) {
	in := append(bytes.Repeat([]byte("xyzabc"), 3000), make([]byte, 1000)...)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ref []byte
	for k := 0; k < 30; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := enc.CompressParallel(in, 409, 3, enc.DefaultConfig())
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if ref == nil {
				ref = out
			} else if !bytes.Equal(ref, out) {
				t.Error("nondeterministic parallel output")
			}
		}()
	}
	wg.Wait()
}
