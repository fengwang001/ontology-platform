package ontology

// Decode reverses Encode. desc must be the same direction slice that
// was passed to NewEncoder (nil means all ascending); when desc is
// nil, keys are decoded until the input is exhausted, otherwise
// exactly len(desc) keys are decoded and any remaining bytes are
// reported as ErrTrailing.
//
// Malformed input never panics and never returns partial results:
// truncation, trailing bytes and invalid tags all produce a
// *DecodeError naming the failing key index.
func Decode(desc []bool, data []byte) ([]any, error) {
	r := &reader{data: data}
	n := len(desc)
	if desc == nil {
		n = -1
	}
	var keys []any
	for i := 0; n < 0 || i < n; i++ {
		if r.done() {
			if n < 0 {
				return keys, nil
			}
			return nil, &DecodeError{KeyIndex: i, Err: ErrTruncated}
		}
		r.flip = desc != nil && i < len(desc) && desc[i]
		k, err := r.readKey(i)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	if !r.done() {
		return nil, &DecodeError{KeyIndex: len(keys), Err: ErrTrailing}
	}
	return keys, nil
}

type reader struct {
	data []byte
	pos  int
	flip bool
}

func (r *reader) done() bool { return r.pos >= len(r.data) }

// raw returns the next byte without applying the direction flip.
func (r *reader) raw(keyIndex int) (byte, error) {
	if r.done() {
		return 0, &DecodeError{KeyIndex: keyIndex, Err: ErrTruncated}
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

// get returns the next byte with the direction flip applied.
func (r *reader) get(keyIndex int) (byte, error) {
	b, err := r.raw(keyIndex)
	if err != nil {
		return 0, err
	}
	if r.flip {
		return ^b, nil
	}
	return b, nil
}

func (r *reader) readKey(keyIndex int) (any, error) {
	// nil is never direction-flipped, so its tag is always literal.
	if r.data[r.pos] == tagNil {
		r.pos++
		return nil, nil
	}
	tag, err := r.get(keyIndex)
	if err != nil {
		return nil, err
	}
	switch tag {
	case tagZero:
		return r.readZero(keyIndex)
	case tagPos:
		return r.readNumber(keyIndex, false)
	case tagNeg:
		return r.readNumber(keyIndex, true)
	case tagStr:
		return r.readString(keyIndex)
	default:
		return nil, &DecodeError{KeyIndex: keyIndex, Err: ErrBadTag}
	}
}

func (r *reader) readZero(keyIndex int) (any, error) {
	tb, err := r.get(keyIndex)
	if err != nil {
		return nil, err
	}
	switch tb {
	case typeInt:
		return int64(0), nil
	case typeFloat:
		return float64(0), nil
	default:
		return nil, &DecodeError{KeyIndex: keyIndex, Err: ErrBadTag}
	}
}

func (r *reader) readNumber(keyIndex int, neg bool) (any, error) {
	hi, err := r.get(keyIndex)
	if err != nil {
		return nil, err
	}
	lo, err := r.get(keyIndex)
	if err != nil {
		return nil, err
	}
	if neg { // the magnitude body is stored complemented
		hi, lo = ^hi, ^lo
	}
	exp := (int(hi)<<8 | int(lo)) - expBias
	mant, err := r.readEscaped(keyIndex, neg)
	if err != nil {
		return nil, err
	}
	// The type byte is never complemented with the magnitude body;
	// only the direction flip (already set on the reader) applies.
	tb, err := r.get(keyIndex)
	if err != nil {
		return nil, err
	}
	switch tb {
	case typeInt:
		mag := recomposeInt(exp, mant)
		if neg {
			return -int64(mag), nil
		}
		return int64(mag), nil
	case typeFloat:
		mag := recomposeFloat(exp, mant)
		if neg {
			return -mag, nil
		}
		return mag, nil
	default:
		return nil, &DecodeError{KeyIndex: keyIndex, Err: ErrBadTag}
	}
}

func (r *reader) readString(keyIndex int) (any, error) {
	bs, err := r.readEscaped(keyIndex, false)
	if err != nil {
		return nil, err
	}
	return string(bs), nil
}

// readEscaped reads bytes until the 0x00 0x00 terminator. For
// negative numbers the magnitude body is complemented, so the
// reader un-complements while scanning.
func (r *reader) readEscaped(keyIndex int, complemented bool) ([]byte, error) {
	var out []byte
	for {
		b, err := r.get(keyIndex)
		if err != nil {
			return nil, err
		}
		if complemented {
			b = ^b
		}
		if b != termByte {
			out = append(out, b)
			continue
		}
		next, err := r.get(keyIndex)
		if err != nil {
			return nil, err
		}
		if complemented {
			next = ^next
		}
		switch next {
		case termByte:
			return out, nil
		case escapeByte:
			out = append(out, termByte)
		default:
			return nil, &DecodeError{KeyIndex: keyIndex, Err: ErrBadTag}
		}
	}
}
