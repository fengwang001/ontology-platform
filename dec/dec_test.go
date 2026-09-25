package dec_test

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"

	"ontology/dec"
	"ontology/enc"
	"ontology/wire"
)

func seal(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	e, _ := enc.New(&buf, enc.Config{WindowCap: 1 << 15, MaxChain: 32})
	e.Write(data)
	e.Close()
	return buf.Bytes()
}

func sumOf(data []byte) uint64 {
	s := wire.SumSeed
	for _, b := range data {
		s = wire.SumByte(s, b)
	}
	return s
}

func TestCorruption(t *testing.T) {
	h := wire.Header()
	lit := func(s string) []byte { return wire.AppendLiteral(nil, []byte(s)) }
	valid := bytes.Join([][]byte{h, lit("hi"), wire.AppendEnd(nil, 2, sumOf([]byte("hi")))}, nil)
	cases := []struct {
		name   string
		stream []byte
		cap    int
		want   error
		off    int64
	}{
		{"magic", []byte{0x00}, 1 << 15, wire.ErrMagic, 0},
		{"version", []byte{wire.Magic, 0x7f}, 1 << 15, wire.ErrVersion, 1},
		{"dist-zero", bytes.Join([][]byte{h, wire.AppendBackref(nil, 0, 3)}, nil), 1 << 15, wire.ErrDistZero, 2},
		{"dist-output", bytes.Join([][]byte{h, lit("ab"), wire.AppendBackref(nil, 3, 1)}, nil), 1 << 15, wire.ErrDistOutput, 5},
		{"dist-window", bytes.Join([][]byte{h, lit("abcd"), wire.AppendBackref(nil, 5, 1)}, nil), 4, wire.ErrDistWindow, 7},
		{"varint", append(append(h, bytes.Repeat([]byte{0x80}, 9)...), 0x02), 1 << 15, wire.ErrVarint, 11},
		{"length", bytes.Join([][]byte{h, lit("hi"), wire.AppendEnd(nil, 99, sumOf([]byte("hi")))}, nil), 1 << 15, wire.ErrLenMismatch, 5},
		{"checksum", bytes.Join([][]byte{h, lit("hi"), wire.AppendEnd(nil, 2, 42)}, nil), 1 << 15, wire.ErrChecksum, 5},
		{"trailing", append(valid, 0x00), 1 << 15, wire.ErrTrailing, int64(len(valid))},
	}
	for _, c := range cases {
		d, _ := dec.New(dec.Config{WindowCap: c.cap})
		_, err := d.Write(c.stream)
		if err == nil {
			err = d.Close()
		}
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
			continue
		}
		var we *wire.Error
		if !errors.As(err, &we) || we.Off != c.off {
			t.Errorf("%s: offset got %+v, want %d", c.name, err, c.off)
		}
	}
}

func TestTruncation(t *testing.T) {
	data := []byte(strings.Repeat("truncate-me-", 30))
	stream := seal(t, data)
	for cut := 0; cut < len(stream); cut++ {
		d, _ := dec.New(dec.Config{WindowCap: 1 << 15})
		d.Write(stream[:cut])
		if err := d.Close(); !errors.Is(err, wire.ErrTruncated) {
			t.Fatalf("cut=%d: got %v", cut, err)
		}
		if !bytes.HasPrefix(data, d.Output()) {
			t.Fatalf("cut=%d: output is not a prefix", cut)
		}
	}
}

func TestFlip(t *testing.T) {
	data := []byte(strings.Repeat("flip me gently. ", 20))
	stream := seal(t, data)
	for i := range stream {
		for bit := 0; bit < 8; bit++ {
			bad := bytes.Clone(stream)
			bad[i] ^= 1 << bit
			d, _ := dec.New(dec.Config{WindowCap: 1 << 15, MaxOutput: 1 << 16})
			d.Write(bad)
			if err := d.Close(); err == nil && !bytes.Equal(d.Output(), data) {
				t.Fatalf("byte %d bit %d: silently wrong output", i, bit)
			}
			if len(d.Output()) > 1<<16 {
				t.Fatalf("byte %d bit %d: output limit breached", i, bit)
			}
		}
	}
}

func TestOutputLimit(t *testing.T) {
	h := wire.Header()
	bomb := wire.AppendUvarint(nil, uint64(1<<40)<<2|wire.TagBackref)
	bomb = wire.AppendUvarint(bomb, 1)
	litBomb := wire.AppendUvarint(nil, uint64(1<<40)<<2|wire.TagLiteral)
	cases := []struct {
		name   string
		stream []byte
		outLen int
		off    int64
	}{
		{"backref", bytes.Join([][]byte{h, wire.AppendLiteral(nil, []byte("a")), bomb}, nil), 1, 4},
		{"literal", append(h, litBomb...), 0, 2},
	}
	for _, c := range cases {
		d, _ := dec.New(dec.Config{WindowCap: 1 << 15, MaxOutput: 1 << 20})
		_, err := d.Write(c.stream)
		if !errors.Is(err, wire.ErrOutputLimit) {
			t.Fatalf("%s: got %v", c.name, err)
		}
		if len(d.Output()) != c.outLen {
			t.Errorf("%s: output %d bytes, want %d (rejected before writing)", c.name, len(d.Output()), c.outLen)
		}
		if _, err2 := d.Write([]byte{0}); err2 != err {
			t.Errorf("%s: terminal error not sticky", c.name)
		}
		var we *wire.Error
		if errors.As(err, &we) && we.Off != c.off {
			t.Errorf("%s: offset %d, want %d", c.name, we.Off, c.off)
		}
	}
}

func TestSplitFeedAndEmpty(t *testing.T) {
	data := []byte(strings.Repeat("split feed ", 25))
	stream := seal(t, data)
	for cut := 0; cut <= len(stream); cut++ {
		d, _ := dec.New(dec.Config{WindowCap: 1 << 15})
		d.Write(stream[:cut])
		d.Write(stream[cut:])
		if err := d.Close(); err != nil || !bytes.Equal(d.Output(), data) {
			t.Fatalf("cut=%d: err=%v", cut, err)
		}
	}
	empty := seal(t, nil)
	if len(empty) == 0 {
		t.Fatal("empty input must produce a non-empty stream")
	}
	d, _ := dec.New(dec.Config{WindowCap: 1 << 15})
	d.Write(empty)
	if err := d.Close(); err != nil || len(d.Output()) != 0 {
		t.Fatalf("empty stream: err=%v out=%d", err, len(d.Output()))
	}
	for _, s := range [][]byte{nil, wire.Header()} {
		z, _ := dec.New(dec.Config{WindowCap: 1 << 15})
		z.Write(s)
		if err := z.Close(); !errors.Is(err, wire.ErrTruncated) {
			t.Fatalf("len=%d stream: got %v, want truncated", len(s), err)
		}
	}
}

func TestConcurrentDecoders(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte(g), byte(g + 1)}, 500)
			stream := seal(t, data)
			d, _ := dec.New(dec.Config{WindowCap: 1 << 15})
			d.Write(stream)
			if err := d.Close(); err != nil || !bytes.Equal(d.Output(), data) {
				t.Errorf("goroutine %d: err=%v", g, err)
			}
		}()
	}
	wg.Wait()
}
