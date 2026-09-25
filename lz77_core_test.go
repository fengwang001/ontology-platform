package lz77_test

import (
	"bytes"
	"math/rand"
	"testing"

	"ontology/dec"
	"ontology/enc"
)

func genData(kind string, n int) []byte {
	b := make([]byte, n)
	switch kind {
	case "same":
		for i := range b {
			b[i] = 'A'
		}
	case "periodic":
		for i := range b {
			b[i] = byte("abc"[i%3])
		}
	case "random":
		r := rand.New(rand.NewSource(42))
		r.Read(b)
	}
	return b
}

func decomp(t *testing.T, stream []byte, maxOut uint64) []byte {
	t.Helper()
	d, err := dec.New(dec.Config{MaxOutput: maxOut})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Write(stream); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return d.Output()
}

func TestRoundtrip(t *testing.T) {
	cases := []struct {
		kind string
		n    int
	}{
		{"empty", 0}, {"same", 1}, {"same", 1000},
		{"periodic", 5000}, {"random", 5000}, {"random", 100000},
	}
	for _, tc := range cases {
		data := genData(tc.kind, tc.n)
		z, err := enc.Compress(data, enc.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if got := decomp(t, z, 1<<30); !bytes.Equal(got, data) {
			t.Fatalf("%s/%d roundtrip mismatch", tc.kind, tc.n)
		}
	}
}

func TestOverlap(t *testing.T) {
	for _, d := range []int{1, 2, 3} {
		data := genData("periodic", 0)
		data = append(data, []byte("abc")[:d]...)
		data = append(data, make([]byte, 64)...)
		for i := d; i < len(data); i++ {
			data[i] = data[i-d]
		}
		z, _ := enc.Compress(data, enc.Config{})
		if got := decomp(t, z, 1<<30); !bytes.Equal(got, data) {
			t.Fatalf("overlap d=%d mismatch", d)
		}
	}
}

func writeChunked(t *testing.T, data []byte, size int) []byte {
	e, _ := enc.New(enc.Config{})
	var out []byte
	for i := 0; i < len(data); i += size {
		t2 := min(i+size, len(data))
		if _, err := e.Write(data[i:t2]); err != nil {
			t.Fatal(err)
		}
		out = append(out, e.Flush()...)
	}
	tail, err := e.Close()
	if err != nil {
		t.Fatal(err)
	}
	return append(out, tail...)
}

func TestWriteChunking(t *testing.T) {
	data := genData("periodic", 10000)
	ref := writeChunked(t, data, len(data))
	for _, sz := range []int{1, 7, 13, 10000} {
		if got := writeChunked(t, data, sz); !bytes.Equal(got, ref) {
			t.Fatalf("chunk size %d differs", sz)
		}
	}
}

func TestFlush(t *testing.T) {
	e, _ := enc.New(enc.Config{})
	e.Write([]byte("hello world"))
	d, _ := dec.New(dec.Config{})
	if _, err := d.Write(e.Flush()); err != nil {
		t.Fatal(err)
	}
	if string(d.Output()) != "hello world" {
		t.Fatalf("flush output %q", d.Output())
	}
	if b := e.Flush(); len(b) != 0 {
		t.Fatalf("second flush emitted %d bytes", len(b))
	}
	e.Write([]byte("hello again world")) // may reference pre-flush bytes
	d2, _ := dec.New(dec.Config{})
	d2.Write([]byte("hello world")) // not needed; reset below
	_ = d2
	tail, _ := e.Close()
	if _, err := d.Write(tail); err != nil {
		t.Fatalf("after flush: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if string(d.Output()) != "hello worldhello again world" {
		t.Fatalf("got %q", d.Output())
	}
}

func TestDecodeChunking(t *testing.T) {
	data := genData("random", 8000)
	z, _ := enc.Compress(data, enc.Config{})
	for step := 1; step <= 37; step++ {
		d, _ := dec.New(dec.Config{})
		for i := 0; i < len(z); i += step {
			j := min(i+step, len(z))
			if _, err := d.Write(z[i:j]); err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
		}
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(d.Output(), data) {
			t.Fatalf("step %d mismatch", step)
		}
	}
}

func TestEmptyAndZero(t *testing.T) {
	z, _ := enc.Compress(nil, enc.Config{})
	if len(z) == 0 {
		t.Fatal("empty input must yield non-empty stream")
	}
	if got := decomp(t, z, 1<<20); len(got) != 0 {
		t.Fatal("empty stream output")
	}
	for _, cut := range [][]byte{nil, z[:len(z)-1]} {
		d, _ := dec.New(dec.Config{})
		d.Write(cut)
		if err := d.Close(); err == nil {
			t.Fatal("expected truncation")
		}
	}
}

func TestParallel(t *testing.T) {
	data := genData("random", 50000)
	data = append(data, genData("periodic", 50000)...)
	var ref []byte
	for _, w := range []int{1, 2, 4, 8} {
		z, err := enc.CompressParallel(data, 4096, w, enc.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if w == 1 {
			ref = z
		} else if !bytes.Equal(z, ref) {
			t.Fatalf("workers %d differ", w)
		}
		if got := decomp(t, z, uint64(len(data))); !bytes.Equal(got, data) {
			t.Fatalf("workers %d roundtrip", w)
		}
	}
}

func TestComplexity(t *testing.T) {
	const chain = 32
	var rates []float64
	var lastSize int
	for _, n := range []int{64 << 10, 4 << 20} {
		for _, kind := range []string{"same", "random"} {
			data := genData(kind, n)
			e, _ := enc.New(enc.Config{MaxChain: chain})
			if _, err := e.Write(data); err != nil {
				t.Fatal(err)
			}
			z, _ := e.Close()
			ex := e.Examined()
			if ex > int64(chain)*int64(n) {
				t.Fatalf("%s/%d examined %d > bound %d", kind, n, ex, chain*n)
			}
			if kind == "same" {
				rates = append(rates, float64(ex)/float64(n))
				lastSize = len(z)
			}
		}
	}
	if rates[1] > 2*rates[0] {
		t.Fatalf("examined rate grew: %v", rates)
	}
	if lastSize >= 64<<10 {
		t.Fatalf("4MB same compressed to %d bytes", lastSize)
	}
}
