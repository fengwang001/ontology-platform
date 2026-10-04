// Package tsblock implements a Gorilla-style compressed time-series block.
//
// A block stores (timestamp, value) samples as a bit stream: a 64-bit
// header holding the block start timestamp, followed by delta-of-delta
// encoded timestamps and XOR-window encoded float64 values, terminated
// by a 36-bit seal marker. Bits are written most-significant-first.
package tsblock

import (
	"errors"
	"math"
	"math/bits"
	"sync"
)

var (
	ErrParam   = errors.New("tsblock: invalid parameter")
	ErrOrder   = errors.New("tsblock: timestamp out of order")
	ErrDelta   = errors.New("tsblock: timestamp delta out of range")
	ErrFull    = errors.New("tsblock: block capacity exceeded")
	ErrSealed  = errors.New("tsblock: block is sealed")
	ErrCorrupt = errors.New("tsblock: corrupt bitstream")
)

const (
	minMaxBytes    = 13
	maxMaxBytes    = 1 << 20
	maxFirstDelta  = 15359
	sealMarkerBits = 36 // '1111' control + 32 zero bits
)

// Sample is a single (timestamp, value) pair.
type Sample struct {
	T int64
	V float64
}

// bitWriter appends bits most-significant-first.
type bitWriter struct {
	buf   []byte
	nbits int
}

func (w *bitWriter) writeBit(bit bool) {
	if w.nbits%8 == 0 {
		w.buf = append(w.buf, 0)
	}
	if bit {
		w.buf[w.nbits/8] |= 1 << (7 - uint(w.nbits%8))
	}
	w.nbits++
}

// writeBits writes the low n bits of u, most significant bit first.
func (w *bitWriter) writeBits(u uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		w.writeBit(u>>uint(i)&1 == 1)
	}
}

// Block is a Gorilla-style compressed time-series block encoder.
// All methods are safe for concurrent use.
type Block struct {
	mu      sync.Mutex
	w       bitWriter
	maxBits int
	sealed  bool
	start   int64
	count   int
	prevT   int64
	prevD   int64
	prevV   uint64
	hasWin  bool
	winL    int
	winT    int
}

// New creates a block with the given start timestamp and byte capacity.
// maxBytes must satisfy 13 <= maxBytes <= 1048576.
func New(start int64, maxBytes int) (*Block, error) {
	if maxBytes < minMaxBytes || maxBytes > maxMaxBytes {
		return nil, ErrParam
	}
	b := &Block{maxBits: maxBytes * 8, start: start}
	b.w.writeBits(uint64(start), 64)
	return b, nil
}

// Bits returns the number of valid bits written so far.
func (b *Block) Bits() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.w.nbits
}

// Len returns the number of samples appended.
func (b *Block) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

// Bytes returns a copy of the bit stream, padded with zero bits to a
// whole byte.
func (b *Block) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]byte, len(b.w.buf))
	copy(out, b.w.buf)
	return out
}

// dodBits returns the number of bits needed to encode dod.
func dodBits(dod int64) int {
	switch {
	case dod == 0:
		return 1
	case dod >= -63 && dod <= 64:
		return 2 + 7
	case dod >= -255 && dod <= 256:
		return 3 + 9
	case dod >= -2047 && dod <= 2048:
		return 4 + 12
	default:
		return 4 + 32
	}
}

// valBits returns the number of bits needed to encode the XOR value x.
func valBits(x uint64, hasWin bool, winL, winT int) int {
	if x == 0 {
		return 1
	}
	lz, tz := windowOf(x)
	if hasWin && lz >= winL && tz >= winT {
		reuse := 2 + (64 - winL - winT)
		reopen := 13 + (64 - lz - tz)
		if reuse <= reopen {
			return reuse
		}
	}
	return 13 + (64 - lz - tz)
}

// windowOf returns the (leading, trailing) zero counts of x, with the
// leading count capped at 31. x must be non-zero.
func windowOf(x uint64) (lz, tz int) {
	lz = bits.LeadingZeros64(x)
	if lz > 31 {
		lz = 31
	}
	return lz, bits.TrailingZeros64(x)
}

func (w *bitWriter) writeDod(dod int64) {
	switch {
	case dod == 0:
		w.writeBit(false)
	case dod >= -63 && dod <= 64:
		w.writeBits(0b10, 2)
		w.writeBits(uint64(dod), 7)
	case dod >= -255 && dod <= 256:
		w.writeBits(0b110, 3)
		w.writeBits(uint64(dod), 9)
	case dod >= -2047 && dod <= 2048:
		w.writeBits(0b1110, 4)
		w.writeBits(uint64(dod), 12)
	default:
		w.writeBits(0b1111, 4)
		w.writeBits(uint64(dod), 32)
	}
}

// writeVal encodes the XOR value x and updates the window on reopen.
func (b *Block) writeVal(x uint64) {
	if x == 0 {
		b.w.writeBit(false)
		return
	}
	lz, tz := windowOf(x)
	if b.hasWin && lz >= b.winL && tz >= b.winT {
		reuse := 2 + (64 - b.winL - b.winT)
		reopen := 13 + (64 - lz - tz)
		if reuse <= reopen {
			b.w.writeBits(0b10, 2)
			b.w.writeBits(x>>uint(b.winT), 64-b.winL-b.winT)
			return
		}
	}
	siglen := 64 - lz - tz
	b.w.writeBits(0b11, 2)
	b.w.writeBits(uint64(lz), 5)
	b.w.writeBits(uint64(siglen%64), 6)
	b.w.writeBits(x>>uint(tz), siglen)
	b.hasWin = true
	b.winL = lz
	b.winT = tz
}

// Append adds a sample to the block. Timestamps must be non-decreasing.
// A rejected append leaves the block state untouched.
func (b *Block) Append(t int64, v float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		return ErrSealed
	}
	vb := math.Float64bits(v)
	if b.count == 0 {
		if t < b.start {
			return ErrDelta
		}
		d0 := t - b.start
		if d0 < 0 || d0 > maxFirstDelta {
			return ErrDelta
		}
		if b.w.nbits+14+64+sealMarkerBits > b.maxBits {
			return ErrFull
		}
		b.w.writeBits(uint64(d0), 14)
		b.w.writeBits(vb, 64)
		b.prevT = t
		b.prevD = d0
		b.prevV = vb
		b.count++
		return nil
	}
	if t < b.prevT {
		return ErrOrder
	}
	d := t - b.prevT
	if d < 0 {
		return ErrDelta // int64 overflow
	}
	dod := d - b.prevD
	if dod < math.MinInt32 || dod > math.MaxInt32 {
		return ErrDelta
	}
	x := vb ^ b.prevV
	need := dodBits(dod) + valBits(x, b.hasWin, b.winL, b.winT)
	if b.w.nbits+need+sealMarkerBits > b.maxBits {
		return ErrFull
	}
	b.w.writeDod(dod)
	b.writeVal(x)
	b.prevT = t
	b.prevD = d
	b.prevV = vb
	b.count++
	return nil
}

// Seal writes the end-of-block marker ('1111' + 32 zero bits). It never
// fails on an unsealed block because Append reserves space for it.
func (b *Block) Seal() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		return ErrSealed
	}
	b.w.writeBits(0b1111, 4)
	b.w.writeBits(0, 32)
	b.sealed = true
	return nil
}
