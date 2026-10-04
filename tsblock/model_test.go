package tsblock

import (
	"bytes"
	"math"
	"math/bits"
	"math/rand"
	"testing"
)

// bitStr is a naive bit-string model: bits are appended one by one,
// most-significant-first, exactly as the specification describes.
type bitStr struct {
	b []bool
}

// w appends the low n bits of u, most significant bit first.
func (s *bitStr) w(u uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		s.b = append(s.b, u>>uint(i)&1 == 1)
	}
}

// bytes packs the bit string MSB-first, padding the tail with zeros.
func (s *bitStr) bytes() []byte {
	out := make([]byte, (len(s.b)+7)/8)
	for i, bit := range s.b {
		if bit {
			out[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return out
}

// modelBlock is a straightforward, per-spec re-implementation used as
// the reference for differential testing against Block.
type modelBlock struct {
	s       bitStr
	maxBits int
	start   int64
	sealed  bool
	count   int
	prevT   int64
	prevD   int64
	prevV   uint64
	hasWin  bool
	winL    int
	winT    int
	samples []Sample
}

func newModel(start int64, maxBytes int) *modelBlock {
	m := &modelBlock{maxBits: maxBytes * 8, start: start}
	m.s.w(uint64(start), 64)
	return m
}

func (m *modelBlock) fits(extra int) bool {
	return len(m.s.b)+extra+36 <= m.maxBits
}

func (m *modelBlock) append(t int64, v float64) error {
	if m.sealed {
		return ErrSealed
	}
	vb := math.Float64bits(v)
	if m.count == 0 {
		if t < m.start {
			return ErrDelta
		}
		d0 := t - m.start
		if d0 < 0 || d0 > 15359 {
			return ErrDelta
		}
		if !m.fits(14 + 64) {
			return ErrFull
		}
		m.s.w(uint64(d0), 14)
		m.s.w(vb, 64)
		m.prevT, m.prevD, m.prevV = t, d0, vb
		m.count++
		m.samples = append(m.samples, Sample{T: t, V: v})
		return nil
	}
	if t < m.prevT {
		return ErrOrder
	}
	d := t - m.prevT
	if d < 0 {
		return ErrDelta
	}
	dod := d - m.prevD
	if dod < math.MinInt32 || dod > math.MaxInt32 {
		return ErrDelta
	}
	x := vb ^ m.prevV
	need := modelDodBits(dod) + m.valBits(x)
	if !m.fits(need) {
		return ErrFull
	}
	m.writeDod(dod)
	m.writeVal(x)
	m.prevT, m.prevD, m.prevV = t, d, vb
	m.count++
	m.samples = append(m.samples, Sample{T: t, V: v})
	return nil
}

func modelDodBits(dod int64) int {
	switch {
	case dod == 0:
		return 1
	case dod >= -63 && dod <= 64:
		return 9
	case dod >= -255 && dod <= 256:
		return 12
	case dod >= -2047 && dod <= 2048:
		return 16
	default:
		return 36
	}
}

func modelWindow(x uint64) (lz, tz int) {
	lz = bits.LeadingZeros64(x)
	if lz > 31 {
		lz = 31
	}
	return lz, bits.TrailingZeros64(x)
}

func (m *modelBlock) valBits(x uint64) int {
	if x == 0 {
		return 1
	}
	lz, tz := modelWindow(x)
	if m.hasWin && lz >= m.winL && tz >= m.winT &&
		2+(64-m.winL-m.winT) <= 13+(64-lz-tz) {
		return 2 + (64 - m.winL - m.winT)
	}
	return 13 + (64 - lz - tz)
}

func (m *modelBlock) writeDod(dod int64) {
	switch {
	case dod == 0:
		m.s.w(0, 1)
	case dod >= -63 && dod <= 64:
		m.s.w(0b10, 2)
		m.s.w(uint64(dod), 7)
	case dod >= -255 && dod <= 256:
		m.s.w(0b110, 3)
		m.s.w(uint64(dod), 9)
	case dod >= -2047 && dod <= 2048:
		m.s.w(0b1110, 4)
		m.s.w(uint64(dod), 12)
	default:
		m.s.w(0b1111, 4)
		m.s.w(uint64(dod), 32)
	}
}

func (m *modelBlock) writeVal(x uint64) {
	if x == 0 {
		m.s.w(0, 1)
		return
	}
	lz, tz := modelWindow(x)
	if m.hasWin && lz >= m.winL && tz >= m.winT &&
		2+(64-m.winL-m.winT) <= 13+(64-lz-tz) {
		m.s.w(0b10, 2)
		m.s.w(x>>uint(m.winT), 64-m.winL-m.winT)
		return
	}
	siglen := 64 - lz - tz
	m.s.w(0b11, 2)
	m.s.w(uint64(lz), 5)
	m.s.w(uint64(siglen%64), 6)
	m.s.w(x>>uint(tz), siglen)
	m.hasWin, m.winL, m.winT = true, lz, tz
}

func (m *modelBlock) seal() {
	m.s.w(0b1111, 4)
	m.s.w(0, 32)
	m.sealed = true
}

// interestingX generates XOR patterns that exercise the window logic.
func interestingX(rng *rand.Rand) uint64 {
	switch rng.Intn(5) {
	case 0:
		return 0
	case 1:
		return rng.Uint64()
	case 2:
		return uint64(1) << uint(rng.Intn(64))
	case 3:
		// Random window with exact lz/tz control.
		lz := rng.Intn(40)
		tz := rng.Intn(40)
		if lz+tz >= 64 {
			return 1
		}
		hi := 63 - lz
		var x uint64
		if hi == 63 {
			x = rng.Uint64()
		} else {
			x = rng.Uint64() & (uint64(1)<<uint(hi+1) - 1)
		}
		x |= uint64(1) << uint(hi)
		x |= uint64(1) << uint(tz)
		x &^= uint64(1)<<uint(tz) - 1
		return x
	default:
		// Patterns near the previous window are produced by the caller
		// via small perturbations; here use sparse low/high bits.
		return rng.Uint64() & (uint64(1)<<uint(rng.Intn(64)) | 1)
	}
}

var specialBits = []uint64{
	0x0000000000000000, // +0.0
	0x8000000000000000, // -0.0
	0x7FF8000000000000, // NaN
	0x7FF8000000000001, // NaN payload
	0xFFF8000000000000, // -NaN
	0x7FF0000000000000, // +Inf
	0xFFF0000000000000, // -Inf
	0x0000000000000001, // min denormal
	0x7FFFFFFFFFFFFFFF, // max int64 pattern
	0x3FF0000000000000, // 1.0
}

// TestRandomModel runs 2000 random operation sequences, comparing the
// Block against the naive bit-string model after every operation and
// verifying a decode round trip at the end.
func TestRandomModel(t *testing.T) {
	for seed := int64(0); seed < 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		start := rng.Int63n(1<<40) - (1 << 39)
		maxBytes := 13 + rng.Intn(200)
		b, err := New(start, maxBytes)
		if err != nil {
			t.Fatalf("seed %d: New: %v", seed, err)
		}
		m := newModel(start, maxBytes)
		t.Logf("seed=%d start=%d maxBytes=%d: begin", seed, start, maxBytes)

		ops := 1 + rng.Intn(30)
		for op := 0; op < ops; op++ {
			ts, v, note := randomOp(rng, m)
			gotErr := b.Append(ts, v)
			wantErr := m.append(ts, v)
			if gotErr != wantErr {
				t.Fatalf("seed %d op %d (%s): Append(%d, %x) err = %v, model = %v",
					seed, op, note, ts, math.Float64bits(v), gotErr, wantErr)
			}
			if b.Bits() != len(m.s.b) {
				t.Fatalf("seed %d op %d (%s): Bits = %d, model = %d",
					seed, op, note, b.Bits(), len(m.s.b))
			}
			if !bytes.Equal(b.Bytes(), m.s.bytes()) {
				t.Fatalf("seed %d op %d (%s): Bytes = %x, model = %x",
					seed, op, note, b.Bytes(), m.s.bytes())
			}
			if b.Len() != m.count {
				t.Fatalf("seed %d op %d (%s): Len = %d, model = %d",
					seed, op, note, b.Len(), m.count)
			}
			t.Logf("seed=%d op=%d %s Append(%d, %x) -> err=%v bits=%d",
				seed, op, note, ts, math.Float64bits(v), gotErr, b.Bits())
		}

		if err := b.Seal(); err != nil {
			t.Fatalf("seed %d: Seal: %v", seed, err)
		}
		m.seal()
		if b.Bits() != len(m.s.b) || !bytes.Equal(b.Bytes(), m.s.bytes()) {
			t.Fatalf("seed %d: sealed stream mismatch: %x vs %x",
				seed, b.Bytes(), m.s.bytes())
		}

		gotStart, samples, err := Decode(b.Bytes())
		if err != nil {
			t.Fatalf("seed %d: Decode: %v", seed, err)
		}
		if gotStart != start {
			t.Fatalf("seed %d: start = %d, want %d", seed, gotStart, start)
		}
		if len(samples) != len(m.samples) {
			t.Fatalf("seed %d: decoded %d samples, want %d",
				seed, len(samples), len(m.samples))
		}
		for i, s := range samples {
			w := m.samples[i]
			if s.T != w.T || math.Float64bits(s.V) != math.Float64bits(w.V) {
				t.Fatalf("seed %d: sample %d = (%d, %x), want (%d, %x)",
					seed, i, s.T, math.Float64bits(s.V), w.T, math.Float64bits(w.V))
			}
		}
		t.Logf("seed=%d ok: %d samples, %d bits, %d bytes, round trip verified",
			seed, len(samples), b.Bits(), len(b.Bytes()))
	}
}

// randomOp produces the next (timestamp, value) operation for the
// model's current state, occasionally generating invalid operations.
func randomOp(rng *rand.Rand, m *modelBlock) (int64, float64, string) {
	// Value: derive from the XOR pattern so window behavior is covered.
	var vb uint64
	if rng.Intn(10) == 0 {
		vb = specialBits[rng.Intn(len(specialBits))]
	} else {
		vb = m.prevV ^ interestingX(rng)
	}
	v := math.Float64frombits(vb)

	if m.count == 0 {
		switch rng.Intn(10) {
		case 0:
			return m.start - 1, v, "first-delta-negative"
		case 1:
			return m.start + 15360 + rng.Int63n(1000), v, "first-delta-too-large"
		case 2:
			return m.start + 15359, v, "first-delta-max"
		default:
			return m.start + rng.Int63n(15360), v, "first-delta-ok"
		}
	}

	switch rng.Intn(20) {
	case 0:
		return m.prevT - 1 - rng.Int63n(100), v, "out-of-order"
	case 1:
		// dod outside int32.
		return m.prevT + m.prevD + math.MaxInt32 + 1 + rng.Int63n(1<<20), v, "dod-overflow"
	}

	var dod int64
	switch rng.Intn(8) {
	case 0:
		dod = 0
	case 1:
		dod = int64(rng.Intn(201)) - 100
	case 2:
		edge := []int64{64, 65, -63, -64, 256, 257, -255, -256,
			2048, 2049, -2047, -2048, math.MaxInt32, math.MinInt32}
		dod = edge[rng.Intn(len(edge))]
	case 3:
		dod = int64(rng.Intn(1<<16)) - (1 << 15)
	case 4:
		dod = rng.Int63n(math.MaxInt32)
	case 5:
		dod = -rng.Int63n(1 << 31)
	default:
		dod = int64(rng.Intn(21)) - 10
	}
	d := m.prevD + dod
	if d < 0 {
		dod = -m.prevD // clamp to d = 0
		d = 0
	}
	return m.prevT + d, v, "ok"
}
