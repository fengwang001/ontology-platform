package ontology_test

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"

	"ontology/dec"
	"ontology/enc"
)

func compress(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	e, err := enc.New(&b, enc.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decode(t *testing.T, z []byte) []byte {
	t.Helper()
	d, err := dec.New(dec.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Write(z); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return d.Output()
}

func TestRoundTripInputs(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	random := make([]byte, 4096)
	r.Read(random)
	periodic := bytes.Repeat([]byte("abcabcXYZ"), 700)
	cases := [][]byte{nil, {}, bytes.Repeat([]byte{7}, 5000), random, periodic}
	for i, in := range cases {
		if got := decode(t, compress(t, in)); !bytes.Equal(got, in) {
			t.Fatalf("case %d mismatch", i)
		}
	}
}

func TestOverlapCopy(t *testing.T) {
	for _, dist := range []int{1, 2, 3} {
		in := append([]byte("abc"), bytes.Repeat([]byte("abc"), 30/dist+10)...)
		_ = dist
		if got := decode(t, compress(t, in)); !bytes.Equal(got, in) {
			t.Fatalf("distance %d mismatch", dist)
		}
	}
}

func TestWriteChunkDeterminism(t *testing.T) {
	in := bytes.Repeat([]byte("abcdefgABC"), 500)
	want := compress(t, in)
	for _, size := range []int{1, 7, len(in)} {
		var b bytes.Buffer
		e, _ := enc.New(&b, enc.Config{})
		for i := 0; i < len(in); i += size {
			end := min(i+size, len(in))
			if _, err := e.Write(in[i:end]); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b.Bytes(), want) {
			t.Fatalf("chunk %d differs", size)
		}
	}
}

func TestFlush(t *testing.T) {
	in := bytes.Repeat([]byte("abc"), 100)
	var b bytes.Buffer
	e, _ := enc.New(&b, enc.Config{})
	if _, err := e.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	mid := b.Len()
	d, _ := dec.New(dec.Config{})
	if _, err := d.Write(b.Bytes()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.Output(), in) {
		t.Fatal("flush did not expose all input")
	}
	if err := e.Flush(); err != nil || b.Len() != mid {
		t.Fatal("second flush emitted bytes")
	}
}

func TestParallelDeterministic(t *testing.T) {
	in := append(bytes.Repeat([]byte("abc"), 30000), make([]byte, 20000)...)
	want := compress(t, in)
	var first []byte
	for _, workers := range []int{1, 2, 4, 8} {
		got, err := enc.CompressParallel(in, 12345, workers)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decode(t, got), in) || !bytes.Equal(got, want) {
			t.Fatalf("workers %d differs", workers)
		}
		first = got
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _ := enc.CompressParallel(in, 12345, 4)
			if !bytes.Equal(got, first) {
				t.Fatal("nondeterministic parallel output")
			}
		}()
	}
	wg.Wait()
}

func TestCandidateComplexity(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	cases := []struct {
		name string
		data []byte
	}{
		{"same64k", bytes.Repeat([]byte{1}, 64<<10)},
		{"same4m", bytes.Repeat([]byte{1}, 4<<20)},
		{"rand4m", func() []byte { p := make([]byte, 4<<20); r.Read(p); return p }()},
	}
	var sameRate []float64
	for _, tc := range cases {
		var b bytes.Buffer
		e, _ := enc.New(&b, enc.Config{})
		_, _ = e.Write(tc.data)
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		n := e.CandidatesExamined()
		if n > int64(enc.ChainLimit)*int64(len(tc.data)) {
			t.Fatalf("%s candidates %d", tc.name, n)
		}
		if tc.name[:4] == "same" {
			sameRate = append(sameRate, float64(n)/float64(len(tc.data)))
			if tc.name == "same4m" && b.Len() >= 64<<10 {
				t.Fatalf("compressed %d", b.Len())
			}
		}
	}
	if sameRate[1] > 2*sameRate[0] {
		t.Fatalf("rates %v", sameRate)
	}
}
