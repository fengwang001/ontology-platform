package ontology

import "math"

type bitReader struct {
	data     []byte
	position int
}

func Decode(data []byte) (int64, []Sample, error) {
	reader := bitReader{data: data}
	startBits, ok := reader.read(64)
	if !ok {
		return 0, nil, ErrCorrupt
	}
	start := int64(startBits)

	var samples []Sample
	var previousTime int64
	var previousDelta int64
	var previousValue uint64
	var hasWindow bool
	var windowLZ int
	var windowTZ int

	for {
		var timestamp int64
		if len(samples) == 0 {
			prefix, ok := reader.read(4)
			if !ok {
				return 0, nil, ErrCorrupt
			}
			if prefix == 0b1111 {
				zeroMarker, ok := reader.read(32)
				if !ok || zeroMarker != 0 {
					return 0, nil, ErrCorrupt
				}
				break
			}
			rest, ok := reader.read(10)
			if !ok {
				return 0, nil, ErrCorrupt
			}
			firstDeltaBits := prefix<<10 | rest
			previousDelta = int64(firstDeltaBits)
			timestamp, ok = addInt64(start, previousDelta)
			if !ok {
				return 0, nil, ErrCorrupt
			}
		} else {
			dod, marker, ok := reader.readTimestampMarker()
			if !ok {
				return 0, nil, ErrCorrupt
			}
			if marker {
				break
			}
			delta, ok := addInt64(previousDelta, dod)
			if !ok {
				return 0, nil, ErrCorrupt
			}
			timestamp, ok = addInt64(previousTime, delta)
			if !ok {
				return 0, nil, ErrCorrupt
			}
			previousDelta = delta
		}

		valueBits, ok := reader.readSampleValue(len(samples) == 0, previousValue, &hasWindow, &windowLZ, &windowTZ)
		if !ok {
			return 0, nil, ErrCorrupt
		}

		previousTime = timestamp
		previousValue = valueBits
		samples = append(samples, Sample{
			Timestamp: timestamp,
			Value:     math.Float64frombits(valueBits),
		})
	}

	remaining := len(data)*8 - reader.position
	if remaining >= 8 {
		return 0, nil, ErrCorrupt
	}
	padding, ok := reader.read(remaining)
	if !ok || padding != 0 {
		return 0, nil, ErrCorrupt
	}
	return start, samples, nil
}

func (r *bitReader) read(width int) (uint64, bool) {
	if width < 0 || width > 64 {
		return 0, false
	}

	var value uint64
	for range width {
		bit, ok := r.readBit()
		if !ok {
			return 0, false
		}
		value = value<<1 | uint64(bit)
	}
	return value, true
}

func (r *bitReader) readBit() (byte, bool) {
	if r.position < 0 || r.position >= len(r.data)*8 {
		return 0, false
	}
	bit := r.data[r.position/8] >> (7 - uint(r.position%8)) & 1
	r.position++
	return bit, true
}

func (r *bitReader) readSigned(width int) (int64, bool) {
	value, ok := r.read(width)
	if !ok {
		return 0, false
	}
	if value > 1<<uint(width-1) {
		value -= 1 << uint(width)
	}
	return int64(value), true
}

func addInt64(a, b int64) (int64, bool) {
	if b > 0 && a > 1<<63-1-b {
		return 0, false
	}
	if b < 0 && a < -1<<63-b {
		return 0, false
	}
	return a + b, true
}

func (r *bitReader) readTimestampMarker() (dod int64, marker bool, ok bool) {
	bit, ok := r.readBit()
	if !ok {
		return 0, false, false
	}
	if bit == 0 {
		return 0, false, true
	}

	if bit, ok = r.readBit(); !ok {
		return 0, false, false
	} else if bit == 0 {
		dod, ok = r.readSigned(7)
		return dod, false, ok
	}

	if bit, ok = r.readBit(); !ok {
		return 0, false, false
	} else if bit == 0 {
		dod, ok = r.readSigned(9)
		return dod, false, ok
	}

	if bit, ok = r.readBit(); !ok {
		return 0, false, false
	} else if bit == 0 {
		dod, ok = r.readSigned(12)
		return dod, false, ok
	}

	value, ok := r.read(32)
	if !ok {
		return 0, false, false
	}
	if value == 0 {
		return 0, true, true
	}
	return int64(int32(uint32(value))), false, true
}

func (r *bitReader) readSampleValue(
	first bool,
	previousValue uint64,
	hasWindow *bool,
	windowLZ, windowTZ *int,
) (uint64, bool) {
	if first {
		return r.read(64)
	}

	control, ok := r.readBit()
	if !ok {
		return 0, false
	}
	if control == 0 {
		return previousValue, true
	}

	control, ok = r.readBit()
	if !ok {
		return 0, false
	}

	var xor uint64
	if control == 0 {
		if !*hasWindow {
			return 0, false
		}
		width := 64 - *windowLZ - *windowTZ
		if width < 1 {
			return 0, false
		}
		payload, ok := r.read(width)
		if !ok {
			return 0, false
		}
		xor = payload << uint(*windowTZ)
	} else {
		lzBits, ok := r.read(5)
		if !ok {
			return 0, false
		}
		lengthBits, ok := r.read(6)
		if !ok {
			return 0, false
		}
		length := int(lengthBits)
		if length == 0 {
			length = 64
		}
		lz := int(lzBits)
		if length < 1 || lz+length > 64 {
			return 0, false
		}
		tz := 64 - lz - length
		payload, ok := r.read(length)
		if !ok {
			return 0, false
		}
		xor = payload << uint(tz)
		*hasWindow = true
		*windowLZ = lz
		*windowTZ = tz
	}
	return previousValue ^ xor, true
}
