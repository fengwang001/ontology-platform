package enc_test

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"ontology/dec"
	"ontology/enc"
)

func fill(r *rand.Rand, b []byte) {
	for i := range b {
		b[i] = byte(r.Uint32())
	}
}

func encode(t *testing.T, data []byte, chunk int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := enc.NewWriter(&buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(data); i += chunk {
		if _, err := w.Write(data[i:min(i+chunk, len(data))]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decode(t *testing.T, stream []byte, chunk int) []byte {
	t.Helper()
	d, err := dec.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(stream); i += chunk {
		if _, err := d.Write(stream[i:min(i+chunk, len(stream))]); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return d.Output()
}

func inputs() []struct {
	name string
	data []byte
} {
	rnd := make([]byte, 64*1024)
	fill(rand.New(rand.NewPCG(1, 2)), rnd)
	return []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"same-byte", bytes.Repeat([]byte("a"), 100000)},
		{"random", rnd},
		{"periodic", bytes.Repeat([]byte("abc"), 40000)},
		{"text", bytes.Repeat([]byte("the quick brown fox. "), 5000)},
	}
}

func TestRoundtripAndChunking(t *testing.T) {
	for _, in := range inputs() {
		ref := encode(t, in.data, 1<<20)
		for _, chunk := range []int{1, 7, 4099} {
			if got := encode(t, in.data, chunk); !bytes.Equal(ref, got) {
				t.Fatalf("%s: write chunk %d changed output", in.name, chunk)
			}
		}
		for _, chunk := range []int{1, 5, 4096, len(ref) + 1} {
			if got := decode(t, ref, chunk); !bytes.Equal(in.data, got) {
				t.Fatalf("%s: decode chunk %d mismatch", in.name, chunk)
			}
		}
	}
}

func TestOverlapBackrefs(t *testing.T) {
	for _, unit := range []string{"a", "ab", "abc"} { // dist 1, 2, 3
		data := bytes.Repeat([]byte(unit), 40000)
		stream := encode(t, data, 1<<20)
		if len(stream) > 200 {
			t.Fatalf("dist %d: no long back-reference used (%d bytes)", len(unit), len(stream))
		}
		if got := decode(t, stream, 1<<20); !bytes.Equal(data, got) {
			t.Fatalf("dist %d: overlap roundtrip failed", len(unit))
		}
	}
}

func TestFlush(t *testing.T) {
	part1 := bytes.Repeat([]byte("hello "), 4)
	part2 := []byte("hello hello world hello")
	var buf bytes.Buffer
	w, _ := enc.NewWriter(&buf, nil)
	w.Write(part1)
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	d, _ := dec.New(nil)
	if _, err := d.Write(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(part1, d.Output()) {
		t.Fatal("flush did not commit all input")
	}
	mark := buf.Len()
	if err := w.Flush(); err != nil || buf.Len() != mark {
		t.Fatal("flush without new input emitted bytes")
	}
	w.Write(part2)
	w.Flush()
	if grew := buf.Len() - mark; grew > len(part2) {
		t.Fatalf("post-flush data not back-referenced (%d bytes)", grew)
	}
	w.Close()
	if _, err := d.Write(buf.Bytes()[mark:]); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if want := append(part1, part2...); !bytes.Equal(want, d.Output()) {
		t.Fatal("post-flush roundtrip mismatch")
	}
}

func TestParallel(t *testing.T) {
	rnd := make([]byte, 300*1024)
	fill(rand.New(rand.NewPCG(3, 4)), rnd)
	data := append(bytes.Repeat([]byte("ontology-"), 30000), rnd...)
	for _, tc := range []struct {
		data []byte
		bs   int
	}{
		{nil, 100}, {[]byte("abc"), 2}, {data, 64 * 1024}, {data, 100000},
	} {
		ref, err := enc.CompressParallel(tc.data, tc.bs, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, workers := range []int{2, 4, 8} {
			got, err := enc.CompressParallel(tc.data, tc.bs, workers)
			if err != nil || !bytes.Equal(ref, got) {
				t.Fatalf("bs=%d workers=%d: output differs", tc.bs, workers)
			}
		}
		for i := 0; i < 30; i++ {
			if got, _ := enc.CompressParallel(tc.data, tc.bs, 8); !bytes.Equal(ref, got) {
				t.Fatalf("bs=%d: run %d differs", tc.bs, i)
			}
		}
		if got := decode(t, ref, 1<<20); !bytes.Equal(tc.data, got) {
			t.Fatalf("bs=%d: parallel stream does not decode", tc.bs)
		}
	}
}
