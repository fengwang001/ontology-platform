package dec_test

import (
	"bytes"
	"errors"
	"hash/crc32"
	"sync"
	"testing"

	"ontology/dec"
	"ontology/enc"
	"ontology/wire"
)

func stream(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := enc.NewWriter(&buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(data)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func run(stream []byte, cfg *dec.Config) ([]byte, error) {
	d, err := dec.New(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := d.Write(stream); err != nil {
		return d.Output(), err
	}
	return d.Output(), d.Close()
}

func TestCorrupt(t *testing.T) {
	good := stream(t, bytes.Repeat([]byte("abcd"), 200))
	badMagic := bytes.Clone(good)
	badMagic[0] = 'X'
	badVer := bytes.Clone(good)
	badVer[3] = 99
	trailing := append(bytes.Clone(good), 0)
	mk := func(rec ...[]byte) []byte {
		return bytes.Join(append([][]byte{wire.Header()}, rec...), nil)
	}
	lit := func(s string) []byte { return wire.AppendLiteral(nil, []byte(s)) }
	end := func(total, crc uint64) []byte { return wire.AppendEnd(nil, total, crc) }
	crcAB := uint64(crc32.ChecksumIEEE([]byte("ab")))
	cases := []struct {
		name string
		in   []byte
		want error
	}{
		{"bad-magic", badMagic, dec.ErrBadMagic},
		{"bad-version", badVer, dec.ErrBadVersion},
		{"dist-zero", mk(wire.AppendMatch(nil, 0, 3)), dec.ErrDistZero},
		{"dist-history", mk(lit("a"), wire.AppendMatch(nil, 5, 2)), dec.ErrDistHistory},
		{"dist-window", mk(lit("abcd"), wire.AppendMatch(nil, 5, 2)), dec.ErrDistWindow},
		{"varint-overflow", mk([]byte{wire.TagLiteral, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}), dec.ErrVarint},
		{"bad-length", mk(lit("ab"), end(5, crcAB)), dec.ErrLength},
		{"bad-checksum", mk(lit("ab"), end(2, 0)), dec.ErrChecksum},
		{"trailing", trailing, dec.ErrTrailing},
	}
	for _, tc := range cases {
		cfg := &dec.Config{Window: 4}
		if tc.name != "dist-window" {
			cfg = nil
		}
		if _, err := run(tc.in, cfg); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestBomb(t *testing.T) {
	in := bytes.Join([][]byte{wire.Header(), wire.AppendLiteral(nil, []byte("a")),
		wire.AppendMatch(nil, 1, 1<<40)}, nil)
	d, _ := dec.New(&dec.Config{Window: 1 << 15, MaxOutput: 100})
	_, err1 := d.Write(in)
	if !errors.Is(err1, dec.ErrOutputLimit) {
		t.Fatalf("got %v", err1)
	}
	if string(d.Output()) != "a" {
		t.Fatal("output not preserved / bomb not rejected early")
	}
	if _, err2 := d.Write([]byte{0}); err2 != err1 {
		t.Fatal("terminal error not sticky")
	}
}

func TestTruncation(t *testing.T) {
	orig := bytes.Repeat([]byte("truncate me! "), 50)
	full := stream(t, orig)
	for cut := 0; cut < len(full); cut++ {
		out, err := run(full[:cut], nil)
		if !errors.Is(err, dec.ErrTruncated) {
			t.Fatalf("cut %d: got %v", cut, err)
		}
		if !bytes.HasPrefix(orig, out) {
			t.Fatalf("cut %d: output not a prefix", cut)
		}
	}
	if out, err := run(full, nil); err != nil || !bytes.Equal(orig, out) {
		t.Fatal("full stream failed")
	}
}

func TestBitFlip(t *testing.T) {
	orig := bytes.Repeat([]byte("flip me, flip me! "), 30)
	full := stream(t, orig)
	cfg := &dec.Config{Window: 1 << 15, MaxOutput: int64(len(orig))}
	for i := range full {
		bad := bytes.Clone(full)
		bad[i] ^= 0x01
		out, err := run(bad, cfg)
		if err == nil && !bytes.Equal(orig, out) {
			t.Fatalf("flip at %d silently corrupted output", i)
		}
		if len(out) > len(orig) {
			t.Fatalf("flip at %d exceeded output limit", i)
		}
	}
}

func TestEmptyVsNull(t *testing.T) {
	if out, err := run(stream(t, nil), nil); err != nil || len(out) != 0 {
		t.Fatal("empty input must be a valid stream")
	}
	for _, in := range [][]byte{nil, wire.Header()} {
		if _, err := run(in, nil); !errors.Is(err, dec.ErrTruncated) {
			t.Fatalf("len=%d: got %v, want truncated", len(in), err)
		}
	}
}

func TestConcurrentDecoders(t *testing.T) {
	streams := [][]byte{
		stream(t, bytes.Repeat([]byte("x"), 5000)),
		stream(t, []byte("hello world hello world")),
		stream(t, nil),
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(s []byte) {
			defer wg.Done()
			if _, err := run(s, nil); err != nil {
				t.Error(err)
			}
		}(streams[i%len(streams)])
	}
	wg.Wait()
}
