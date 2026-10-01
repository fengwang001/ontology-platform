package bencode_test

// 朴素整体解码器：一次性拿到全部字节，递归下降解析。
// 与流式实现相互独立，用于交叉对照值序列、消费字节数与错误偏移。

import (
	"bytes"
	"errors"

	"ontology/bencode"
)

var errIncomplete = errors.New("incomplete input")

// naiveDecode 解析整个 data，返回完整顶层值序列、已完成值消费的字节数与错误。
// 输入不完整时返回已完成的值与消费数，错误为 nil。
func naiveDecode(data []byte, maxStr uint64, maxDepth int) ([]any, int64, error) {
	var vals []any
	pos := 0
	for pos < len(data) {
		v, next, err := naiveValue(data, pos, 1, maxStr, maxDepth)
		if err == errIncomplete {
			return vals, int64(pos), nil
		}
		if err != nil {
			return vals, int64(pos), err
		}
		vals = append(vals, v)
		pos = next
	}
	return vals, int64(pos), nil
}

func naiveValue(data []byte, pos, depth int, maxStr uint64, maxDepth int) (any, int, error) {
	b := data[pos]
	switch {
	case b == 'i':
		return naiveInt(data, pos)
	case b >= '0' && b <= '9':
		return naiveString(data, pos, maxStr)
	case b == 'l' || b == 'd':
		if depth > maxDepth {
			return nil, 0, &bencode.Error{Kind: bencode.ErrDepthExceeded, Offset: int64(pos), Byte: b}
		}
		if b == 'l' {
			items := []any{}
			p := pos + 1
			for {
				if p >= len(data) {
					return nil, 0, errIncomplete
				}
				if data[p] == 'e' {
					return items, p + 1, nil
				}
				v, next, err := naiveValue(data, p, depth+1, maxStr, maxDepth)
				if err != nil {
					return nil, 0, err
				}
				items = append(items, v)
				p = next
			}
		}
		pairs := bencode.Dict{}
		p := pos + 1
		var last []byte
		hasLast := false
		for {
			if p >= len(data) {
				return nil, 0, errIncomplete
			}
			if data[p] == 'e' {
				return pairs, p + 1, nil
			}
			if data[p] < '0' || data[p] > '9' {
				return nil, 0, &bencode.Error{Kind: bencode.ErrKeyNotString, Offset: int64(p), Byte: data[p]}
			}
			key, next, err := naiveString(data, p, maxStr)
			if err != nil {
				return nil, 0, err
			}
			if hasLast {
				switch c := bytes.Compare(key, last); {
				case c == 0:
					return nil, 0, &bencode.Error{Kind: bencode.ErrDuplicateKey, Offset: int64(p), Byte: data[p]}
				case c < 0:
					return nil, 0, &bencode.Error{Kind: bencode.ErrKeyOrder, Offset: int64(p), Byte: data[p]}
				}
			}
			last, hasLast = key, true
			if next >= len(data) {
				return nil, 0, errIncomplete
			}
			v, after, err := naiveValue(data, next, depth+1, maxStr, maxDepth)
			if err != nil {
				return nil, 0, err
			}
			pairs = append(pairs, bencode.Pair{Key: key, Value: v})
			p = after
		}
	default:
		return nil, 0, &bencode.Error{Kind: bencode.ErrIllegalFirstByte, Offset: int64(pos), Byte: b}
	}
}

func naiveInt(data []byte, pos int) (any, int, error) {
	p := pos + 1
	neg := false
	if p < len(data) && data[p] == '-' {
		neg = true
		p++
	}
	var acc uint64
	digits := 0
	zero := false
	var zeroOff int64
	for {
		if p >= len(data) {
			return nil, 0, errIncomplete
		}
		c := data[p]
		switch {
		case c >= '0' && c <= '9':
			if zero {
				return nil, 0, &bencode.Error{Kind: bencode.ErrIntLeadingZero, Offset: int64(p), Byte: c}
			}
			digit := uint64(c - '0')
			limit := uint64(1<<63 - 1)
			if neg {
				limit = 1 << 63
			}
			if digit > limit || acc > (limit-digit)/10 {
				return nil, 0, &bencode.Error{Kind: bencode.ErrIntOverflow, Offset: int64(p), Byte: c}
			}
			acc = acc*10 + digit
			digits++
			if acc == 0 {
				zero = true
				zeroOff = int64(p)
			}
			p++
		case c == 'e':
			if digits == 0 {
				return nil, 0, &bencode.Error{Kind: bencode.ErrSyntax, Offset: int64(p), Byte: c}
			}
			if zero && neg {
				return nil, 0, &bencode.Error{Kind: bencode.ErrNegativeZero, Offset: zeroOff, Byte: '0'}
			}
			v := int64(acc)
			if neg {
				v = int64(-acc)
			}
			return v, p + 1, nil
		default:
			return nil, 0, &bencode.Error{Kind: bencode.ErrSyntax, Offset: int64(p), Byte: c}
		}
	}
}

func naiveString(data []byte, pos int, maxStr uint64) ([]byte, int, error) {
	p := pos
	var acc uint64
	zero := false
	for {
		if p >= len(data) {
			return nil, 0, errIncomplete
		}
		c := data[p]
		switch {
		case c >= '0' && c <= '9':
			if zero {
				return nil, 0, &bencode.Error{Kind: bencode.ErrLenLeadingZero, Offset: int64(p), Byte: c}
			}
			digit := uint64(c - '0')
			if digit > maxStr || acc > (maxStr-digit)/10 {
				return nil, 0, &bencode.Error{Kind: bencode.ErrLenTooLarge, Offset: int64(p), Byte: c}
			}
			acc = acc*10 + digit
			if acc == 0 {
				zero = true
			}
			p++
		case c == ':':
			p++
			if uint64(len(data)-p) < acc {
				return nil, 0, errIncomplete
			}
			s := make([]byte, acc)
			copy(s, data[p:p+int(acc)])
			return s, p + int(acc), nil
		default:
			return nil, 0, &bencode.Error{Kind: bencode.ErrSyntax, Offset: int64(p), Byte: c}
		}
	}
}
