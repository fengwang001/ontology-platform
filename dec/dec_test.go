package dec_test

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/dec"
	"ontology/enc"
	"ontology/wire"
)

func comp(data []byte) []byte {
	var buf bytes.Buffer
	e, _ := enc.New(&buf, enc.Config{Window: 1 << 16, Chain: 128})
	e.Write(data)
	e.Close()
	return buf.Bytes()
}

func decodeStream(t *testing.T, s []byte, max uint64, cap int) error {
	t.Helper()
	d, err := dec.New(max, cap)
	if err != nil {
		return err
	}
	if _, err := d.Write(s); err != nil {
		return err
	}
	return d.Close()
}

func TestCorruption(t *testing.T) {
	hdr := wire.AppendHeader(nil)
	litAB := wire.AppendLiteral(nil, []byte("ab"))
	end := wire.AppendEnd(nil, 2, wire.SumBytes([]byte("ab")))
	badMagic := comp([]byte("abcabc"))
	badMagic[0] ^= 0xff
	badVer := comp([]byte("abcabc"))
	badVer[3] = 0x99
	longVarint := append(append([]byte{}, hdr...), wire.TagLiteral)
	longVarint = append(longVarint, bytes.Repeat([]byte{0x80}, 11)...)
	cases := []struct {
		name   string
		stream []byte
		cap    int
		want   error
	}{
		{"magic", badMagic, 1 << 16, dec.ErrMagic},
		{"version", badVer, 1 << 16, dec.ErrVersion},
		{"distzero", append(append([]byte{}, hdr...), wire.AppendRef(nil, 0, 1)...), 1 << 16, dec.ErrDistZero},
		{"disttoofar", append(append(append([]byte{}, hdr...), litAB...), wire.AppendRef(nil, 5, 1)...), 1 << 16, dec.ErrDistTooFar},
		{"distwindow", append(append(append([]byte{}, hdr...), litAB...), wire.AppendRef(nil, 100, 1)...), 8, dec.ErrDistWindow},
		{"varint", longVarint, 1 << 16, dec.ErrVarint},
		{"lenmismatch", append(append(append([]byte{}, hdr...), litAB...), wire.AppendEnd(nil, 3, wire.SumBytes([]byte("ab")))...), 1 << 16, dec.ErrLenMismatch},
		{"checksum", append(append(append([]byte{}, hdr...), litAB...), wire.AppendEnd(nil, 2, 0xdead)...), 1 << 16, dec.ErrChecksum},
		{"trailing", append(append(append(append([]byte{}, hdr...), litAB...), end...), 0), 1 << 16, dec.ErrTrailing},
	}
	for _, c := range cases {
		err := decodeStream(t, c.stream, 0, c.cap)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
			continue
		}
		var de *dec.Error
		if !errors.As(err, &de) || de.Off < 0 || de.Off >= int64(len(c.stream)) {
			t.Errorf("%s: bad offset in %v", c.name, err)
		}
	}
}

func TestEmptyVsZeroLength(t *testing.T) {
	s := comp(nil)
	if len(s) == 0 {
		t.Fatal("empty input must produce a non-empty stream")
	}
	if err := decodeStream(t, s, 0, 1<<16); err != nil {
		t.Fatalf("empty stream: %v", err)
	}
	for name, s := range map[string][]byte{"zero": nil, "headerOnly": wire.AppendHeader(nil)} {
		if err := decodeStream(t, s, 0, 1<<16); !errors.Is(err, dec.ErrTruncated) {
			t.Errorf("%s: got %v, want ErrTruncated", name, err)
		}
	}
}

func TestTruncation(t *testing.T) {
	orig := []byte("hello hello hello hello hello")
	s := comp(orig)
	for cut := 0; cut < len(s); cut++ {
		d, _ := dec.New(0, 1<<16)
		d.Write(s[:cut])
		if err := d.Close(); !errors.Is(err, dec.ErrTruncated) {
			t.Fatalf("cut %d: got %v", cut, err)
		}
		if !bytes.Equal(d.Output(), orig[:len(d.Output())]) {
			t.Fatalf("cut %d: output not a prefix", cut)
		}
	}
	if err := decodeStream(t, s, 0, 1<<16); err != nil {
		t.Fatalf("full stream: %v", err)
	}
}

func TestBitFlip(t *testing.T) {
	orig := []byte("flip me flip me flip me!")
	s := comp(orig)
	for i := range s {
		for bit := 0; bit < 8; bit++ {
			bad := append([]byte{}, s...)
			bad[i] ^= 1 << uint(bit)
			d, _ := dec.New(uint64(len(orig)), 1<<16)
			_, err1 := d.Write(bad)
			err2 := d.Close()
			if len(d.Output()) > len(orig) {
				t.Fatalf("byte %d bit %d: output limit breached", i, bit)
			}
			if err1 == nil && err2 == nil && !bytes.Equal(d.Output(), orig) {
				t.Fatalf("byte %d bit %d: silent corruption", i, bit)
			}
		}
	}
}

func TestBomb(t *testing.T) {
	s := append(append(wire.AppendHeader(nil), wire.AppendLiteral(nil, []byte("a"))...), wire.AppendRef(nil, 1, 1<<40)...)
	d, _ := dec.New(1000, 1<<16)
	_, err := d.Write(s)
	if !errors.Is(err, dec.ErrTooBig) {
		t.Fatalf("got %v, want ErrTooBig", err)
	}
	if len(d.Output()) != 1 { // 写出之前拒绝，已输出内容保留
		t.Fatalf("output %d bytes, want 1", len(d.Output()))
	}
	if _, err2 := d.Write([]byte{0}); err2 != err {
		t.Fatalf("terminal error not sticky: %v", err2)
	}
}

func TestChunkSplit(t *testing.T) {
	orig := []byte("split split split split")
	s := comp(orig)
	for cut := 0; cut <= len(s); cut++ {
		d, _ := dec.New(0, 1<<16)
		d.Write(s[:cut])
		d.Write(s[cut:])
		if err := d.Close(); err != nil || !bytes.Equal(d.Output(), orig) {
			t.Fatalf("cut %d: err=%v out=%q", cut, err, d.Output())
		}
	}
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			orig := bytes.Repeat([]byte(fmt.Sprintf("w%d", g)), 5000)
			if err := decodeStream(t, comp(orig), 0, 1<<16); err != nil {
				t.Error(err)
			}
		}(g)
	}
	wg.Wait()
}

func TestBadConfig(t *testing.T) {
	if _, err := dec.New(0, 0); err == nil {
		t.Error("window 0 must be rejected")
	}
}
