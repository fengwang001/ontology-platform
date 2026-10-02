package ontology

import (
	"errors"
	"math"
	"math/bits"
	"sync"
)

var (
	ErrParam   = errors.New("ontology: invalid parameter")
	ErrDelta   = errors.New("ontology: invalid timestamp delta")
	ErrOrder   = errors.New("ontology: timestamps are out of order")
	ErrFull    = errors.New("ontology: block is full")
	ErrSealed  = errors.New("ontology: block is sealed")
	ErrCorrupt = errors.New("ontology: corrupt block")
)

type Sample struct {
	Timestamp int64
	Value     float64
}

type Block struct {
	mu         sync.Mutex
	start      int64
	maxBits    int
	data       []byte
	bits       int
	sealed     bool
	count      int
	lastTime   int64
	lastDelta  int64
	lastValue  uint64
	hasWindow  bool
	prefixLZ   int
	trailingTZ int
}

func New(start int64, maxBytes int) (*Block, error) {
	if maxBytes < 13 || maxBytes > 1048576 {
		return nil, ErrParam
	}

	b := &Block{start: start, maxBits: maxBytes * 8}
	b.writeBits(uint64(start), 64)
	return b, nil
}

func (b *Block) Append(timestamp int64, value float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.sealed {
		return ErrSealed
	}

	valueBits := math.Float64bits(value)
	var added int
	var delta int64
	var dod int64

	if b.count == 0 {
		var ok bool
		delta, ok = subtractInt64(timestamp, b.start)
		if !ok || delta < 0 || delta > 15359 {
			return ErrDelta
		}
		added = 14 + 64
	} else {
		if timestamp < b.lastTime {
			return ErrOrder
		}

		var ok bool
		delta, ok = subtractInt64(timestamp, b.lastTime)
		if !ok {
			return ErrDelta
		}
		dod, ok = subtractInt64(delta, b.lastDelta)
		if !ok || dod < -1<<31 || dod > 1<<31-1 {
			return ErrDelta
		}
		added = timestampBits(dod) + valueBitsToAppend(
			b.lastValue, valueBits, b.hasWindow, b.prefixLZ, b.trailingTZ,
		)
	}

	if b.bits+added+36 > b.maxBits {
		return ErrFull
	}

	if b.count == 0 {
		b.writeBits(uint64(delta), 14)
		b.writeBits(valueBits, 64)
	} else {
		b.writeTimestampDOD(dod)
		b.appendValueBits(valueBits)
	}

	b.lastTime = timestamp
	b.lastDelta = delta
	b.lastValue = valueBits
	b.count++
	return nil
}

func (b *Block) Seal() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		return ErrSealed
	}
	b.writeBits(0b1111, 4)
	b.writeBits(0, 32)
	b.sealed = true
	return nil
}

func (b *Block) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]byte, len(b.data))
	copy(result, b.data)
	return result
}

func (b *Block) Bits() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bits
}

func (b *Block) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

func (b *Block) writeBit(bit byte) {
	if b.bits%8 == 0 {
		b.data = append(b.data, 0)
	}
	if bit != 0 {
		b.data[b.bits/8] |= 1 << (7 - uint(b.bits%8))
	}
	b.bits++
}

func (b *Block) writeBits(value uint64, width int) {
	for i := width - 1; i >= 0; i-- {
		if value&(1<<uint(i)) != 0 {
			b.writeBit(1)
		} else {
			b.writeBit(0)
		}
	}
}

func subtractInt64(a, b int64) (int64, bool) {
	result := a - b
	if (b > 0 && a < result) || (b < 0 && a > result) {
		return 0, false
	}
	return result, true
}

func timestampBits(dod int64) int {
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

func (b *Block) writeTimestampDOD(dod int64) {
	switch {
	case dod == 0:
		b.writeBit(0)
	case dod >= -63 && dod <= 64:
		b.writeBits(0b10, 2)
		b.writeBits(uint64(dod), 7)
	case dod >= -255 && dod <= 256:
		b.writeBits(0b110, 3)
		b.writeBits(uint64(dod), 9)
	case dod >= -2047 && dod <= 2048:
		b.writeBits(0b1110, 4)
		b.writeBits(uint64(dod), 12)
	default:
		b.writeBits(0b1111, 4)
		b.writeBits(uint64(dod), 32)
	}
}

func valueBitsToAppend(previousValue, value uint64, hasWindow bool, windowLZ, windowTZ int) int {
	if value == previousValue {
		return 1
	}

	xor := value ^ previousValue
	lz := min(bits.LeadingZeros64(xor), 31)
	tz := bits.TrailingZeros64(xor)
	if hasWindow && lz >= windowLZ && tz >= windowTZ {
		reuseCost := 2 + 64 - windowLZ - windowTZ
		openCost := 13 + 64 - lz - tz
		if openCost >= reuseCost {
			return reuseCost
		}
	}
	return 13 + 64 - lz - tz
}

func (b *Block) appendValueBits(value uint64) {
	if value == b.lastValue {
		b.writeBit(0)
		return
	}

	xor := value ^ b.lastValue
	lz := min(bits.LeadingZeros64(xor), 31)
	tz := bits.TrailingZeros64(xor)
	reuse := b.hasWindow && lz >= b.prefixLZ && tz >= b.trailingTZ
	if reuse {
		reuseCost := 2 + 64 - b.prefixLZ - b.trailingTZ
		openCost := 13 + 64 - lz - tz
		if openCost < reuseCost {
			reuse = false
		}
	}

	if reuse {
		b.writeBits(0b10, 2)
		width := 64 - b.prefixLZ - b.trailingTZ
		b.writeBits(xor>>b.trailingTZ, width)
		return
	}

	b.writeBits(0b11, 2)
	b.writeBits(uint64(lz), 5)
	length := 64 - lz - tz
	if length == 64 {
		b.writeBits(0, 6)
	} else {
		b.writeBits(uint64(length), 6)
	}
	b.writeBits(xor>>tz, length)
	b.hasWindow = true
	b.prefixLZ = lz
	b.trailingTZ = tz
}
