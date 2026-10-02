package bencode

import (
	"math"
	"strings"
)

// This file holds a deliberately naive whole-input decoder used only by
// the tests as an independent reference. Unlike the streaming parser it
// scans whole tokens first and validates them afterwards, so the two
// implementations only agree if both are correct.

// naiveDecode decodes data as a sequence of top-level values and reports
// the first violation as (reason, offset). consumed is the number of
// bytes that formed complete top-level values before the failure (or the
// full input on success).
func naiveDecode(data []byte, opts Options) (vals []*Value, reason error, offset, consumed int64) {
	o := opts.withDefaults()
	pos := 0
	for pos < len(data) {
		v, next, r, off := naiveValue(data, pos, o, 1)
		if r != nil {
			return vals, r, off, int64(pos)
		}
		vals = append(vals, v)
		pos = next
	}
	return vals, nil, 0, int64(pos)
}

func naiveValue(data []byte, pos int, o Options, depth int) (v *Value, next int, reason error, off int64) {
	b := data[pos]
	switch {
	case b == 'i':
		i := pos + 1
		neg := false
		if i < len(data) && data[i] == '-' {
			neg = true
			i++
		}
		start := i
		for i < len(data) && isDigit(data[i]) {
			i++
		}
		digits := data[start:i]
		if len(digits) > 1 && digits[0] == '0' {
			return nil, 0, ErrIntLeadingZero, int64(start + 1)
		}
		if i >= len(data) {
			return nil, 0, ErrTruncated, int64(i)
		}
		if data[i] != 'e' {
			return nil, 0, ErrSyntax, int64(i)
		}
		if len(digits) == 0 {
			return nil, 0, ErrSyntax, int64(i)
		}
		if neg && len(digits) == 1 && digits[0] == '0' {
			return nil, 0, ErrNegativeZero, int64(start)
		}
		limit := uint64(math.MaxInt64)
		if neg {
			limit = 1 << 63
		}
		var mag uint64
		for k, c := range digits {
			d := uint64(c - '0')
			if mag > (limit-d)/10 {
				return nil, 0, ErrIntOverflow, int64(start + k)
			}
			mag = mag*10 + d
		}
		v = &Value{Kind: KindInt}
		switch {
		case !neg:
			v.Int = int64(mag)
		case mag == 1<<63:
			v.Int = math.MinInt64
		default:
			v.Int = -int64(mag)
		}
		return v, i + 1, nil, 0
	case isDigit(b):
		i := pos
		for i < len(data) && isDigit(data[i]) {
			i++
		}
		digits := data[pos:i]
		if len(digits) > 1 && digits[0] == '0' {
			return nil, 0, ErrLenLeadingZero, int64(pos + 1)
		}
		if i >= len(data) {
			return nil, 0, ErrTruncated, int64(i)
		}
		if data[i] != ':' {
			return nil, 0, ErrSyntax, int64(i)
		}
		var length uint64
		for k, c := range digits {
			d := uint64(c - '0')
			if d > o.MaxString || length > (o.MaxString-d)/10 {
				return nil, 0, ErrStringTooLong, int64(pos + k)
			}
			length = length*10 + d
		}
		body := i + 1
		if uint64(len(data)-body) < length {
			return nil, 0, ErrTruncated, int64(body)
		}
		return &Value{Kind: KindString, Str: string(data[body : body+int(length)])}, body + int(length), nil, 0
	case b == 'l' || b == 'd':
		if depth > o.MaxDepth {
			return nil, 0, ErrDepthExceeded, int64(pos)
		}
		i := pos + 1
		if b == 'l' {
			lv := &Value{Kind: KindList}
			for {
				if i >= len(data) {
					return nil, 0, ErrTruncated, int64(i)
				}
				if data[i] == 'e' {
					return lv, i + 1, nil, 0
				}
				item, nextPos, r, o2 := naiveValue(data, i, o, depth+1)
				if r != nil {
					return nil, 0, r, o2
				}
				lv.List = append(lv.List, item)
				i = nextPos
			}
		}
		dv := &Value{Kind: KindDict}
		prev := ""
		hasPrev := false
		for {
			if i >= len(data) {
				return nil, 0, ErrTruncated, int64(i)
			}
			if data[i] == 'e' {
				return dv, i + 1, nil, 0
			}
			if !isDigit(data[i]) {
				return nil, 0, ErrKeyNotString, int64(i)
			}
			keyOff := i
			keyVal, nextPos, r, o2 := naiveValue(data, i, o, depth+1)
			if r != nil {
				return nil, 0, r, o2
			}
			key := keyVal.Str
			if hasPrev {
				switch strings.Compare(prev, key) {
				case 0:
					return nil, 0, ErrDuplicateKey, int64(keyOff)
				case 1:
					return nil, 0, ErrKeyOutOfOrder, int64(keyOff)
				}
			}
			val, afterVal, r, o2 := naiveValue(data, nextPos, o, depth+1)
			if r != nil {
				return nil, 0, r, o2
			}
			dv.Dict = append(dv.Dict, DictEntry{Key: key, Val: val})
			prev, hasPrev = key, true
			i = afterVal
		}
	default:
		return nil, 0, ErrBadLeadingByte, int64(pos)
	}
}
