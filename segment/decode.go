package segment

import (
	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

// groupHead 是行组块头部的解析结果。
type groupHead struct {
	rows  int
	nulls int
	enc   Encoding
	dict  *dict.Dict
}

// parseGroupHeader 解析行组头部：行数、空值数、编码、字典。
func (s *Segment) parseGroupHeader(p *parser) (groupHead, error) {
	var h groupHead
	p.stage = StageHeader
	rows, err := p.uvarint()
	if err != nil {
		return h, err
	}
	nulls, err := p.uvarint()
	if err != nil {
		return h, err
	}
	if rows > uint64(s.rows) || nulls > rows {
		return h, p.fail("implausible row/null counts")
	}
	h.rows, h.nulls = int(rows), int(nulls)
	enc, err := p.byte()
	if err != nil {
		return h, err
	}
	h.enc = Encoding(enc)
	switch h.enc {
	case EncBitpack:
	case EncDict:
		dlen, err := p.uvarint()
		if err != nil {
			return h, err
		}
		if dlen > uint64(p.limit-p.pos) {
			return h, p.fail("dictionary overruns block")
		}
		raw, err := p.bytes(int(dlen))
		if err != nil {
			return h, err
		}
		d, consumed, err := dict.Thaw(raw, 0)
		if err != nil || consumed != int(dlen) {
			return h, p.fail("malformed dictionary")
		}
		h.dict = d
	default:
		return h, p.fail("unknown encoding")
	}
	return h, nil
}

// parseGroupStats 解析统计区：hasValue 标志 + min/max。
func parseGroupStats(p *parser) (zone.Stats, error) {
	var st zone.Stats
	p.stage = StageStats
	flags, err := p.byte()
	if err != nil {
		return st, err
	}
	if flags > 1 {
		return st, p.fail("bad stats flags")
	}
	if flags == 1 {
		st.HasValue = true
		if st.Min, err = p.svarint(); err != nil {
			return st, err
		}
		if st.Max, err = p.svarint(); err != nil {
			return st, err
		}
		if st.Min > st.Max {
			return st, p.fail("min greater than max")
		}
	}
	return st, nil
}

// parseNullBitmap 解析空值位图，长度必须与行数精确一致。
func parseNullBitmap(p *parser, rows int) ([]byte, error) {
	p.stage = StageNullBitmap
	blen, err := p.uvarint()
	if err != nil {
		return nil, err
	}
	if int(blen) != (rows+7)/8 {
		return nil, p.fail("null bitmap length mismatch")
	}
	return p.bytes(int(blen))
}

// DecodeGroup 解码行组 g 的全部行。任何损坏都返回错误，
// 绝不返回半截结果。
func (s *Segment) DecodeGroup(g int) ([]Value, error) {
	p, err := s.groupParser(g)
	if err != nil {
		return nil, err
	}
	h, err := s.parseGroupHeader(p)
	if err != nil {
		return nil, err
	}
	st, err := parseGroupStats(p)
	if err != nil {
		return nil, err
	}
	bm, err := parseNullBitmap(p, h.rows)
	if err != nil {
		return nil, err
	}
	nonNull, err := s.decodeData(p, &h, &st)
	if err != nil {
		return nil, err
	}
	out := make([]Value, h.rows)
	next := 0
	for i := 0; i < h.rows; i++ {
		if bitmapHas(bm, i) {
			out[i] = Value{Null: true}
		} else {
			out[i] = Value{V: nonNull[next]}
			next++
		}
	}
	return out, nil
}

// decodeData 解析并解码数据块，返回非空值序列。
func (s *Segment) decodeData(p *parser, h *groupHead, st *zone.Stats) ([]int64, error) {
	p.stage = StageData
	dlen, err := p.uvarint()
	if err != nil {
		return nil, err
	}
	raw, err := p.bytes(int(dlen))
	if err != nil {
		return nil, err
	}
	nonNull := h.rows - h.nulls
	if nonNull == 0 {
		if len(raw) != 0 {
			return nil, p.fail("unexpected data for all-null group")
		}
		return nil, nil
	}
	if len(raw) < 1 {
		return nil, p.fail("missing width byte")
	}
	width := int(raw[0])
	payload := raw[1:]
	if width < 1 || width > 64 || bitpack.PackedLen(width, nonNull) != len(payload) {
		return nil, p.fail("data block length mismatch")
	}
	words, err := bitpack.DecodeAll(payload, width, nonNull)
	if err != nil {
		return nil, p.fail(err.Error())
	}
	out := make([]int64, nonNull)
	switch h.enc {
	case EncBitpack:
		for i, off := range words {
			out[i] = int64(uint64(st.Min) + off)
		}
	case EncDict:
		for i, c := range words {
			v, err := h.dict.Value(uint32(c))
			if err != nil {
				return nil, p.fail("code out of dictionary range")
			}
			out[i] = v
		}
	}
	return out, nil
}
