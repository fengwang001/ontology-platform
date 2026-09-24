// Package chunk serializes record sets into a byte stream, splits it into
// fixed-size blocks, and provides a rolling weak checksum plus a strong
// checksum. Weak checksums can advance in O(1) per byte.
package chunk

import (
	"bytes"
	"encoding/binary"
	"sort"

	"ontology/verify"
)

// Mod is the weak-checksum modulus.
const Mod = 65521

// Record is one entry of a record set.
type Record struct {
	Key   []byte
	Value []byte
}

// Marshal serializes records into a canonical key-sorted byte stream.
// Duplicate keys are rejected.
func Marshal(records []Record) ([]byte, error) {
	sorted := make([]Record, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i].Key, sorted[j].Key) < 0
	})
	var buf bytes.Buffer
	var last []byte
	for i := range sorted {
		if i > 0 && bytes.Equal(sorted[i].Key, last) {
			return nil, verify.ErrSig
		}
		last = sorted[i].Key
		var head [4]byte
		binary.BigEndian.PutUint32(head[:], uint32(len(sorted[i].Key)))
		buf.Write(head[:])
		buf.Write(sorted[i].Key)
		binary.BigEndian.PutUint32(head[:], uint32(len(sorted[i].Value)))
		buf.Write(head[:])
		buf.Write(sorted[i].Value)
	}
	return buf.Bytes(), nil
}

// Unmarshal parses a stream produced by Marshal.
func Unmarshal(b []byte) (records []Record, err error) {
	for len(b) > 0 {
		if len(b) < 4 {
			return nil, verify.ErrSig
		}
		klen := int(binary.BigEndian.Uint32(b[:4]))
		b = b[4:]
		if klen > len(b) {
			return nil, verify.ErrSig
		}
		key := append([]byte(nil), b[:klen]...)
		b = b[klen:]
		if len(b) < 4 {
			return nil, verify.ErrSig
		}
		vlen := int(binary.BigEndian.Uint32(b[:4]))
		b = b[4:]
		if vlen > len(b) {
			return nil, verify.ErrSig
		}
		records = append(records, Record{Key: key, Value: append([]byte(nil), b[:vlen]...)})
		b = b[vlen:]
	}
	return records, nil
}

// Weak computes the rolling weak checksum of b, returning (weak, basicOps).
// a = Σx mod M, b = Σ((L-i)*x) mod M, weak = a<<16 | b.
func Weak(b []byte) (uint32, int) {
	var a, s int
	for i, x := range b {
		a = (a + int(x)) % Mod
		s = (s + (len(b)-i)*int(x)) % Mod
	}
	return uint32(a)<<16 | uint32(s), 4 * len(b)
}

// Strong returns the SHA-256 digest of b.
func Strong(b []byte) [32]byte { return verify.StrongSum(b) }

// Blocks splits data into consecutive blocks of size blockSize; the final
// block may be shorter. An empty input yields no blocks.
func Blocks(data []byte, blockSize int) [][]byte {
	if blockSize <= 0 {
		panic(verify.ErrBlockSize)
	}
	var out [][]byte
	for len(data) > 0 {
		n := blockSize
		if n > len(data) {
			n = len(data)
		}
		out = append(out, append([]byte(nil), data[:n]...))
		data = data[n:]
	}
	return out
}

// Roller maintains a sliding window weak checksum. Ops counts basic
// arithmetic operations so that rolling cost can be bounded by 4*len(data).
type Roller struct {
	size           int
	a, s           int
	ops            int
	window         []byte
	initialized    bool
}

// NewRoller creates a roller with the given window (block) size.
func NewRoller(blockSize int) *Roller {
	if blockSize <= 0 {
		panic(verify.ErrBlockSize)
	}
	return &Roller{size: blockSize}
}

// Reset starts a fresh window over b.
func (r *Roller) Reset(b []byte) uint32 {
	w, _ := Weak(b)
	r.a = int(w >> 16)
	r.s = int(w & 0xffff)
	r.ops += 4*len(b) + 2
	r.window = append(r.window[:0], b...)
	r.initialized = true
	return w
}

// Advance drops the leftmost byte and adds xnew, returning the new weak value.
func (r *Roller) Advance(xold, xnew byte) uint32 {
	na := r.a - int(xold) + int(xnew)
	na %= Mod
	if na < 0 {
		na += Mod
	}
	ns := r.s - r.size*int(xold) + na
	ns %= Mod
	if ns < 0 {
		ns += Mod
	}
	r.a, r.s = na, ns
	r.ops += 4
	return uint32(na)<<16 | uint32(ns)
}

// Ops reports the accumulated number of basic weak-checksum operations.
func (r *Roller) Ops() int { return r.ops }
