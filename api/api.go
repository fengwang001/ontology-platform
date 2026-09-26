// Package api is the unified entry point. SelfCheck verifies the four
// invariants on a built-in vector set: round-trip identity, canonical
// fixed point (non-minimal inputs rejected), byte-level agreement with a
// naive textbook encoder, and failure-leaves-no-trace on the reader.
package api

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/enc"
	"ontology/stream"
)

// Re-exported unified entry points.
var (
	EncodeUint = enc.EncodeUint
	EncodeInt  = enc.EncodeInt
	DecodeUint = enc.DecodeUint
	DecodeInt  = enc.DecodeInt
	NewReader  = stream.NewReader
	NewWriter  = stream.NewWriter
)

// naiveEncodeUint is the textbook reference: emit 7-bit groups with the
// continuation bit on every group but the last; no canonicality logic.
func naiveEncodeUint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v&0x7f)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

var uintVecs = []uint64{0, 1, 127, 128, 300, 16383, 16384, 2097151,
	1<<32 - 1, 1 << 63, 1<<64 - 1}

var intVecs = []int64{0, 1, -1, 63, 64, -64, -65, 8191, -8192,
	1<<31 - 1, -1 << 31, 1<<62 - 1, -1 << 62, 1<<63 - 1, -1 << 63}

// SelfCheck verifies all four invariants; nil means every check passed.
func SelfCheck() error {
	// Invariants 1+3: round-trip and naive-reference byte agreement.
	for _, v := range uintVecs {
		b := enc.EncodeUint(v)
		if !bytes.Equal(b, naiveEncodeUint(v)) {
			return fmt.Errorf("api: EncodeUint(%d) != naive reference", v)
		}
		got, n, err := enc.DecodeUint(b)
		if err != nil || got != v || n != len(b) {
			return fmt.Errorf("api: uint round-trip failed for %d", v)
		}
	}
	for _, v := range intVecs {
		b := enc.EncodeInt(v)
		got, n, err := enc.DecodeInt(b)
		if err != nil || got != v || n != len(b) {
			return fmt.Errorf("api: int round-trip failed for %d", v)
		}
	}
	// Invariant 2: non-minimal encodings are rejected with the sentinel.
	for _, bad := range [][]byte{{0x80, 0x00}, {0x80, 0x80, 0x00}} {
		if _, _, err := enc.DecodeUint(bad); !errors.Is(err, enc.ErrNonCanonical) {
			return fmt.Errorf("api: non-canonical %x accepted", bad)
		}
	}
	for _, bad := range [][]byte{{0x80, 0x00}, {0xff, 0x7f}} {
		if _, _, err := enc.DecodeInt(bad); !errors.Is(err, enc.ErrNonCanonical) {
			return fmt.Errorf("api: non-canonical %x accepted", bad)
		}
	}
	// Invariant 4: a rejected read leaves the cursor untouched.
	r := stream.NewReader([]byte{0x2a, 0x80, 0x00})
	if _, err := r.ReadUint(); err != nil || r.Pos() != 1 {
		return errors.New("api: reader setup failed")
	}
	if _, err := r.ReadUint(); !errors.Is(err, enc.ErrNonCanonical) || r.Pos() != 1 {
		return errors.New("api: rejected read advanced the cursor")
	}
	return nil
}
