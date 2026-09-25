package lz77_test

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

func corruptCases(z []byte) []struct {
	name string
	want error
	data []byte
} {
	base := make([]byte, len(z))
	copy(base, z)
	mk := func(name string, mutate func([]byte) []byte, want error) struct {
		name string
		want error
		data []byte
	} {
		return struct {
			name string
			want error
			data []byte
		}{name, want, mutate(append([]byte{}, base...))}
	}
	badVer := append([]byte{}, base...)
	badVer[4] = 9
	zeroDist := append(wire.Header[:], wire.TagMatch, 0x00, 0x03, wire.TagEnd, 0, 0)
	bigDist := append(wire.Header[:], wire.TagMatch, 0x80, 0x80, 0x10, 0x03, wire.TagEnd, 0, 0)
	winDist := append(wire.Header[:], wire.TagMatch, 0x05, 0x03, wire.TagEnd, 0, 0)
	longVar := append(wire.Header[:], wire.TagMatch)
	longVar = append(longVar, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00)
	longVar = append(longVar, 0x03, wire.TagEnd, 0, 0)
	end := bytes.LastIndexByte(base, wire.TagEnd)
	badSize := append([]byte{}, base[:end]...)
	badSize = wire.AppendEnd(badSize, 1, uint64(crc32.ChecksumIEEE(genData("periodic", 201))))
	badCRC := append([]byte{}, base...)
	badCRC[len(badCRC)-1] ^= 0xFF
	return []struct {
		name string
		want error
		data []byte
	}{
		mk("header", func(b []byte) []byte { return badVer }, dec.ErrBadHeader),
		mk("zeroDist", func(b []byte) []byte { return zeroDist }, dec.ErrZeroDistance),
		mk("distOutput", func(b []byte) []byte { return bigDist }, dec.ErrDistanceOutput),
		mk("distWindow", func(b []byte) []byte { return winDist }, dec.ErrDistanceWindow),
		mk("varint", func(b []byte) []byte { return longVar }, wire.ErrVarintTooLong),
		mk("size", func(b []byte) []byte { return badSize }, dec.ErrSizeMismatch),
		mk("crc", func(b []byte) []byte { return badCRC }, dec.ErrChecksum),
		mk("trailing", func(b []byte) []byte { return append(b, 0) }, dec.ErrTrailingBytes),
	}
}

func TestCorruption(t *testing.T) {
	data := append(genData("periodic", 200), 0)
	z, _ := enc.Compress(data, enc.Config{})
	for _, tc := range corruptCases(z) {
		d, _ := dec.New(dec.Config{})
		_, err := d.Write(tc.data)
		if err == nil {
			err = d.Close()
		}
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
		}
		var oe *dec.OffsetError
		if !errors.As(err, &oe) {
			t.Fatalf("%s: error lacks offset", tc.name)
		}
	}
}

func TestBadConfig(t *testing.T) {
	for _, c := range []enc.Config{{WindowCap: 0, MaxChain: 1}, {MaxChain: 0}} {
		if _, err := enc.New(c); err == nil {
			t.Fatal("expected config error")
		}
	}
	if _, err := dec.New(dec.Config{WindowCap: -1}); err == nil {
		t.Fatal("expected window error")
	}
}

func TestBomb(t *testing.T) {
	z := append(wire.Header[:], wire.TagMatch)
	z = wire.AppendUvarint(z, 1)
	z = wire.AppendUvarint(z, 1<<40)
	z = wire.AppendEnd(z, 1<<40, 0)
	d, _ := dec.New(dec.Config{MaxOutput: 1 << 20})
	err := d.Write(z)
	if !errors.Is(err, dec.ErrOutputLimit) {
		t.Fatalf("got %v", err)
	}
	if len(d.Output()) != 0 {
		t.Fatal("bytes emitted before limit check")
	}
	if _, err2 := d.Write([]byte{0}); !errors.Is(err2, dec.ErrOutputLimit) {
		t.Fatalf("terminal error not sticky: %v", err2)
	}
}

func TestTruncation(t *testing.T) {
	data := genData("periodic", 3000)
	z, _ := enc.Compress(data, enc.Config{})
	for i := 0; i < len(z); i++ {
		d, _ := dec.New(dec.Config{})
		err := d.Write(z[:i])
		if err == nil {
			err = d.Close()
		}
		if !errors.Is(err, dec.ErrTruncated) {
			t.Fatalf("cut %d: %v", i, err)
		}
		if !bytes.HasPrefix(data, d.Output()) {
			t.Fatalf("cut %d output not a prefix", i)
		}
	}
}

func TestBitFlip(t *testing.T) {
	data := genData("random", 300)
	z, _ := enc.Compress(data, enc.Config{})
	known := []error{
		dec.ErrBadHeader, dec.ErrZeroDistance, dec.ErrDistanceOutput,
		dec.ErrDistanceWindow, wire.ErrVarintTooLong, wire.ErrVarintOverflow,
		dec.ErrSizeMismatch, dec.ErrChecksum, dec.ErrTrailingBytes,
		dec.ErrBadLength,
	}
	for i := range z {
		for bit := 0; bit < 8; bit++ {
			bad := append([]byte{}, z...)
			bad[i] ^= 1 << bit
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic at %d/%d: %v", i, bit, r)
					}
				}()
				d, _ := dec.New(dec.Config{MaxOutput: uint64(len(data)) + 1})
				err := d.Write(bad)
				if err == nil {
					err = d.Close()
				}
				if err == nil {
					t.Fatalf("flip %d/%d silently accepted", i, bit)
				}
				ok := false
				for _, k := range known {
					if errors.Is(err, k) {
						ok = true
					}
				}
				if !ok {
					t.Fatalf("flip %d/%d unexpected error %v", i, bit, err)
				}
			}()
		}
	}
}

func TestConcurrency(t *testing.T) {
	data := append(genData("random", 20000), genData("same", 20000)...)
	var wg sync.WaitGroup
	results := make([][]byte, 30)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			z, err := enc.CompressParallel(data, 3333, 4, enc.Config{})
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = z
			d, _ := dec.New(dec.Config{})
			if _, err := d.Write(z); err != nil {
				t.Error(err)
			}
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	for i := 1; i < len(results); i++ {
		if !bytes.Equal(results[i], results[0]) {
			t.Fatalf("run %d differs", i)
		}
	}
}
