package enc_test

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"ontology/dec"
	"ontology/enc"
	"ontology/match"
	"ontology/window"
)

var (
	ecfg = enc.Config{WindowCap: 1 << 15, MaxChain: 32}
	dcfg = dec.Config{WindowCap: 1 << 15}
)

func compress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	e, err := enc.New(&buf, ecfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decompress(t *testing.T, stream []byte) []byte {
	t.Helper()
	d, err := dec.New(dcfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Write(stream); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return d.Output()
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	rnd := make([]byte, 4096)
	rng.Read(rnd)
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"same", bytes.Repeat([]byte{7}, 100000)},
		{"random", rnd},
		{"periodic", bytes.Repeat([]byte("abc"), 3000)},
		{"text", []byte(strings.Repeat("the quick brown fox. ", 500))},
	}
	for _, c := range cases {
		got := decompress(t, compress(t, c.data))
		if !bytes.Equal(got, c.data) {
			t.Errorf("%s: round trip mismatch (%d vs %d bytes)", c.name, len(got), len(c.data))
		}
	}
	if s := compress(t, nil); len(s) == 0 {
		t.Error("empty input must produce a non-empty stream")
	}
}

func TestOverlapBackref(t *testing.T) {
	for _, period := range []int{1, 2, 3} {
		data := bytes.Repeat([]byte("abc")[:period], 3000)
		s := compress(t, data)
		if got := decompress(t, s); !bytes.Equal(got, data) {
			t.Errorf("period %d: mismatch", period)
		}
		if len(s) > 100 {
			t.Errorf("period %d: compressed %d bytes, want long backref", period, len(s))
		}
	}
}

func TestWriteSplitting(t *testing.T) {
	data := []byte(strings.Repeat("splitting-independent-output! ", 40))
	flushAt := 63 // 须同时是 1 与 7 的倍数，两种切法才会在同一偏移 Flush
	encode := func(chunk int, flush bool) []byte {
		var buf bytes.Buffer
		e, _ := enc.New(&buf, ecfg)
		for off := 0; off < len(data); {
			n := min(chunk, len(data)-off)
			e.Write(data[off : off+n])
			off += n
			if flush && off == flushAt {
				e.Flush()
			}
		}
		e.Close()
		return buf.Bytes()
	}
	whole := encode(len(data), false)
	for _, chunk := range []int{1, 7} {
		if got := encode(chunk, false); !bytes.Equal(got, whole) {
			t.Errorf("chunk=%d: output differs without flush", chunk)
		}
		if got := encode(chunk, true); !bytes.Equal(got, encode(7, true)) {
			t.Errorf("chunk=%d: output differs with flush@%d", chunk, flushAt)
		}
	}
}

func TestFlushPromise(t *testing.T) {
	data := []byte(strings.Repeat("flush-promise-data ", 20))
	var buf bytes.Buffer
	e, _ := enc.New(&buf, ecfg)
	e.Write(data[:10])
	e.Flush()
	d, _ := dec.New(dcfg)
	d.Write(buf.Bytes())
	if !bytes.Equal(d.Output(), data[:10]) {
		t.Fatal("flush must make all input so far decodable")
	}
	n := buf.Len()
	e.Flush()
	if buf.Len() != n {
		t.Fatal("second flush without new input must emit nothing")
	}
	e.Write(data[10:])
	e.Close()
	if got := decompress(t, buf.Bytes()); !bytes.Equal(got, data) {
		t.Fatal("full stream mismatch after flush")
	}
}

func TestLongBackref(t *testing.T) {
	if s := compress(t, make([]byte, 4<<20)); len(s) >= 64<<10 {
		t.Fatalf("4MB same-byte compressed to %d, want < 64KB", len(s))
	}
}

func TestCompressParallel(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	data := make([]byte, 200<<10)
	for i := range data {
		if i%3 == 0 {
			data[i] = byte(i % 7)
		} else {
			data[i] = byte(rng.Intn(256))
		}
	}
	base, err := enc.CompressParallel(data, 4096, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []int{2, 4, 8} {
		got, _ := enc.CompressParallel(data, 4096, w)
		if !bytes.Equal(got, base) {
			t.Fatalf("workers=%d differs", w)
		}
	}
	for i := 0; i < 30; i++ {
		got, _ := enc.CompressParallel(data, 4096, 8)
		if !bytes.Equal(got, base) {
			t.Fatal("run", i, "differs")
		}
	}
	if got := decompress(t, base); !bytes.Equal(got, data) {
		t.Fatal("parallel stream does not decode")
	}
	empty, _ := enc.CompressParallel(nil, 4096, 4)
	if got := decompress(t, empty); len(got) != 0 {
		t.Fatal("empty parallel input must decode to empty")
	}
}

func TestConfigRejected(t *testing.T) {
	var buf bytes.Buffer
	w, _ := window.New(1)
	cases := []struct {
		name string
		fn   func() error
	}{
		{"window", func() error { _, err := window.New(0); return err }},
		{"chain", func() error { _, err := match.New(w, 0); return err }},
		{"enc-window", func() error { _, err := enc.New(&buf, enc.Config{WindowCap: 0, MaxChain: 1}); return err }},
		{"enc-chain", func() error { _, err := enc.New(&buf, enc.Config{WindowCap: 1, MaxChain: 0}); return err }},
		{"dec-window", func() error { _, err := dec.New(dec.Config{WindowCap: 0}); return err }},
	}
	for _, c := range cases {
		if err := c.fn(); err == nil {
			t.Errorf("%s: zero config not rejected", c.name)
		}
	}
}
