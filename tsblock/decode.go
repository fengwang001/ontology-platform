package tsblock

import "math"

// bitReader reads bits most-significant-first.
type bitReader struct {
	data []byte
	pos  int
}

func (r *bitReader) readBits(n int) (uint64, bool) {
	if n < 0 || r.pos+n > len(r.data)*8 {
		return 0, false
	}
	var u uint64
	for i := 0; i < n; i++ {
		u = u<<1 | uint64(r.data[r.pos/8]>>(7-uint(r.pos%8))&1)
		r.pos++
	}
	return u, true
}

func (r *bitReader) peekBits(n int) (uint64, bool) {
	saved := r.pos
	u, ok := r.readBits(n)
	r.pos = saved
	return u, ok
}

// signExtend interprets u as a k-bit two's complement value: values
// greater than 2^(k-1) are reduced by 2^k.
func signExtend(u uint64, k uint) int64 {
	if u > 1<<(k-1) {
		return int64(u) - 1<<k
	}
	return int64(u)
}

// Decode parses a sealed block bit stream and returns the block start
// timestamp and the decoded samples.
func Decode(data []byte) (int64, []Sample, error) {
	r := &bitReader{data: data}
	su, ok := r.readBits(64)
	if !ok {
		return 0, nil, ErrCorrupt
	}
	start := int64(su)
	var samples []Sample

	hdr, ok := r.peekBits(4)
	if !ok {
		return 0, nil, ErrCorrupt
	}
	if hdr == 0b1111 {
		// Zero-sample block: the marker follows the header directly.
		// A first sample can never start with '1111' because d0 <= 15359.
		if err := readMarker(r); err != nil {
			return 0, nil, err
		}
		return start, samples, checkTail(r)
	}

	d0, ok := r.readBits(14)
	if !ok {
		return 0, nil, ErrCorrupt
	}
	v, ok := r.readBits(64)
	if !ok {
		return 0, nil, ErrCorrupt
	}
	prevT := start + int64(d0)
	prevD := int64(d0)
	prevV := v
	samples = append(samples, Sample{T: prevT, V: math.Float64frombits(v)})

	hasWin := false
	var winL, winT int
	for {
		dod, marker, err := readDod(r)
		if err != nil {
			return 0, nil, err
		}
		if marker {
			return start, samples, checkTail(r)
		}
		d := prevD + dod
		t := prevT + d
		x, err := readVal(r, &hasWin, &winL, &winT)
		if err != nil {
			return 0, nil, err
		}
		cur := prevV ^ x
		prevT, prevD, prevV = t, d, cur
		samples = append(samples, Sample{T: t, V: math.Float64frombits(cur)})
	}
}

// readMarker consumes the already-peeked '1111' plus 32 zero bits.
func readMarker(r *bitReader) error {
	r.pos += 4
	z, ok := r.readBits(32)
	if !ok || z != 0 {
		return ErrCorrupt
	}
	return nil
}

// checkTail verifies that fewer than 8 bits remain after the marker and
// that all of them are zero padding.
func checkTail(r *bitReader) error {
	if len(r.data)*8-r.pos >= 8 {
		return ErrCorrupt
	}
	for r.pos < len(r.data)*8 {
		u, _ := r.readBits(1)
		if u != 0 {
			return ErrCorrupt
		}
	}
	return nil
}

// readDod reads one delta-of-delta control prefix and value. It reports
// marker=true when the 36-bit end-of-block marker was consumed.
func readDod(r *bitReader) (dod int64, marker bool, err error) {
	u, ok := r.readBits(1)
	if !ok {
		return 0, false, ErrCorrupt
	}
	if u == 0 {
		return 0, false, nil
	}
	if u, ok = r.readBits(1); !ok {
		return 0, false, ErrCorrupt
	}
	if u == 0 { // '10'
		v, ok := r.readBits(7)
		if !ok {
			return 0, false, ErrCorrupt
		}
		return signExtend(v, 7), false, nil
	}
	if u, ok = r.readBits(1); !ok {
		return 0, false, ErrCorrupt
	}
	if u == 0 { // '110'
		v, ok := r.readBits(9)
		if !ok {
			return 0, false, ErrCorrupt
		}
		return signExtend(v, 9), false, nil
	}
	if u, ok = r.readBits(1); !ok {
		return 0, false, ErrCorrupt
	}
	if u == 0 { // '1110'
		v, ok := r.readBits(12)
		if !ok {
			return 0, false, ErrCorrupt
		}
		return signExtend(v, 12), false, nil
	}
	// '1111'
	v, ok := r.readBits(32)
	if !ok {
		return 0, false, ErrCorrupt
	}
	if v == 0 {
		return 0, true, nil
	}
	return int64(int32(v)), false, nil
}

// readVal reads one XOR-encoded value, updating the window on reopen.
func readVal(r *bitReader, hasWin *bool, winL, winT *int) (uint64, error) {
	u, ok := r.readBits(1)
	if !ok {
		return 0, ErrCorrupt
	}
	if u == 0 {
		return 0, nil
	}
	if u, ok = r.readBits(1); !ok {
		return 0, ErrCorrupt
	}
	if u == 0 { // '10': reuse existing window
		if !*hasWin {
			return 0, ErrCorrupt
		}
		n := 64 - *winL - *winT
		v, ok := r.readBits(n)
		if !ok {
			return 0, ErrCorrupt
		}
		return v << uint(*winT), nil
	}
	// '11': open a new window
	lz, ok := r.readBits(5)
	if !ok {
		return 0, ErrCorrupt
	}
	sl, ok := r.readBits(6)
	if !ok {
		return 0, ErrCorrupt
	}
	siglen := int(sl)
	if siglen == 0 {
		siglen = 64
	}
	tz := 64 - int(lz) - siglen
	if tz < 0 {
		return 0, ErrCorrupt
	}
	v, ok := r.readBits(siglen)
	if !ok {
		return 0, ErrCorrupt
	}
	*hasWin = true
	*winL = int(lz)
	*winT = tz
	return v << uint(tz), nil
}
