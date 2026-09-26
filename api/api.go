// Package api is the single external entry point for the VLQ/LEB128 codec.
// It re-exports the enc/stream primitives and provides SelfCheck, which
// verifies the package invariants on a built-in set of vectors.
package api

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"ontology/enc"
	"ontology/stream"
)

// EncodeUint re-exports enc.EncodeUint.
func EncodeUint(v uint64) []byte { return enc.EncodeUint(v) }

// EncodeInt re-exports enc.EncodeInt.
func EncodeInt(v int64) []byte { return enc.EncodeInt(v) }

// DecodeUint re-exports enc.DecodeUint.
func DecodeUint(b []byte) (uint64, int, error) { return enc.DecodeUint(b) }

// DecodeInt re-exports enc.DecodeInt.
func DecodeInt(b []byte) (int64, int, error) { return enc.DecodeInt(b) }

// NewReader re-exports stream.NewReader.
func NewReader(b []byte) *stream.Reader { return stream.NewReader(b) }

// NewWriter re-exports stream.NewWriter.
func NewWriter() *stream.Writer { return stream.NewWriter() }

// SelfCheck verifies the four invariants on built-in vectors (reference
// encodings, round-trips, naive-encoder identity, rejected reads with a
// stable cursor, single-pass consumption, atomic concurrent writes). It
// holds no package state and is safe for concurrent use.
func SelfCheck() error {
	vectors := []struct {
		v uint64
		b []byte
	}{
		{0, []byte{0x00}}, {1, []byte{0x01}}, {127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}}, {300, []byte{0xac, 0x02}},
		{16383, []byte{0xff, 0x7f}}, {16384, []byte{0x80, 0x80, 0x01}},
		{2097151, []byte{0xff, 0xff, 0x7f}},
	}
	for _, c := range vectors {
		if got := enc.EncodeUint(c.v); !bytes.Equal(got, c.b) {
			return fmt.Errorf("vector %d: % x != % x", c.v, got, c.b)
		}
		if u, n, err := enc.DecodeUint(c.b); err != nil || u != c.v || n != len(c.b) {
			return fmt.Errorf("vector %d roundtrip: %d %d %v", c.v, u, n, err)
		}
	}
	// (3) byte-identity with a textbook naive encoder, plus round-trips.
	naive := func(v uint64) []byte {
		o := []byte{}
		for v >= 0x80 {
			o = append(o, byte(v&0x7f)|0x80)
			v >>= 7
		}
		return append(o, byte(v))
	}
	for i := uint64(0); i < 3000; i++ {
		u := i*2862933555777941757 + 3037000493
		bu := enc.EncodeUint(u)
		if !bytes.Equal(bu, naive(u)) {
			return fmt.Errorf("naive mismatch at %d", u)
		}
		if gu, n, e := enc.DecodeUint(bu); e != nil || gu != u || n != len(bu) {
			return fmt.Errorf("uint rt %d", u)
		}
		s, bs := int64(u), enc.EncodeInt(int64(u))
		if gs, n, e := enc.DecodeInt(bs); e != nil || gs != s || n != len(bs) {
			return fmt.Errorf("int rt %d", s)
		}
	}
	// (3) distinct sentinel failures; (4) cursor survives rejection.
	r := stream.NewReader([]byte{0x05, 0x80, 0x00})
	if v, err := r.ReadUint(); err != nil || v != 5 {
		return fmt.Errorf("lead read: %d %v", v, err)
	}
	if _, err := r.ReadUint(); !errors.Is(err, enc.ErrNonCanonical) || r.Pos() != 1 {
		return fmt.Errorf("non-canonical: %v pos %d", err, r.Pos())
	}
	r.Reset([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80})
	if _, err := r.ReadUint(); !errors.Is(err, enc.ErrOverflow) || r.Pos() != 0 {
		return fmt.Errorf("overflow: %v pos %d", err, r.Pos())
	}
	r.Reset(nil)
	if _, err := r.ReadUint(); !errors.Is(err, enc.ErrEmpty) || r.Pos() != 0 {
		return fmt.Errorf("empty: %v pos %d", err, r.Pos())
	}
	// Single-pass: m values decode with total consumed == buffer length.
	const m = 5000
	var buf []byte
	for i := 0; i < m; i++ {
		buf = append(buf, enc.EncodeUint(uint64(i)*7919+13)...)
	}
	r.Reset(buf)
	for i := 0; i < m; i++ {
		if v, err := r.ReadUint(); err != nil || v != uint64(i)*7919+13 {
			return fmt.Errorf("linear i=%d: %d %v", i, v, err)
		}
	}
	if r.Pos() != len(buf) {
		return fmt.Errorf("linear: pos %d != len %d", r.Pos(), len(buf))
	}
	// Atomic concurrent writes: the concatenation decodes back to exactly
	// the multiset of values written (no interleaved multi-byte varint).
	const n = 128
	w := stream.NewWriter()
	var wg sync.WaitGroup
	want := map[uint64]int{}
	for i := 0; i < n; i++ {
		v := uint64(i)*1_000_003 + 999999
		want[v]++
		wg.Add(1)
		go func(x uint64) { defer wg.Done(); w.WriteUint(x) }(v)
	}
	wg.Wait()
	got := map[uint64]int{}
	rr := stream.NewReader(w.Bytes())
	for rr.Pos() < rr.Len() {
		v, err := rr.ReadUint()
		if err != nil {
			return fmt.Errorf("concurrent decode at %d: %v", rr.Pos(), err)
		}
		got[v]++
	}
	if len(got) != len(want) {
		return fmt.Errorf("concurrent: got %d distinct, want %d", len(got), len(want))
	}
	for v, c := range want {
		if got[v] != c {
			return fmt.Errorf("concurrent: value %d count %d want %d", v, got[v], c)
		}
	}
	return nil
}
