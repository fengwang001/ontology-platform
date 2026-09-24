// Package seg 实现记录编码/解码、截断恢复与单段内按键查询。不依赖其他包。
package seg

import (
	"encoding/binary"
	"errors"
)

// ErrCorrupt 表示段损坏：非法 tag、长度溢出或首条记录即不完整。
var ErrCorrupt = errors.New("seg: corrupt segment")

// errIncomplete 表示记录被截断（非首条时可丢弃恢复）。
var errIncomplete = errors.New("seg: incomplete record")

const (
	TagPut byte = 0
	TagDel byte = 1
)

// Record 是一条写入记录；Del 为 true 时是墓碑（Val 为空）。
type Record struct {
	Key string
	Val string
	Del bool
}

// Segment 是不可变段：ID 严格递增，Level 为层号。
type Segment struct {
	ID    int
	Level int
	Recs  []Record
}

// Encode 把段序列化为记录序列，每条 = uvarint(klen) key 1字节tag uvarint(vlen) val。
func Encode(s Segment) []byte {
	var buf []byte
	for _, r := range s.Recs {
		buf = binary.AppendUvarint(buf, uint64(len(r.Key)))
		buf = append(buf, r.Key...)
		tag := TagPut
		if r.Del {
			tag = TagDel
		}
		buf = append(buf, tag)
		buf = binary.AppendUvarint(buf, uint64(len(r.Val)))
		buf = append(buf, r.Val...)
	}
	return buf
}

// LoadSegment 从头解码，遇第一条不完整记录即停并丢弃（返回丢弃条数）；
// 首条即不完整、非法 tag、长度溢出返回 ErrCorrupt。空输入 = 空段、丢弃 0。
func LoadSegment(b []byte) (Segment, int, error) {
	var s Segment
	for len(b) > 0 {
		r, n, err := decodeOne(b)
		if err != nil {
			if errors.Is(err, errIncomplete) && len(s.Recs) > 0 {
				return s, 1, nil
			}
			return Segment{}, 0, ErrCorrupt
		}
		s.Recs = append(s.Recs, r)
		b = b[n:]
	}
	return s, 0, nil
}

func decodeOne(b []byte) (Record, int, error) {
	klen, n := binary.Uvarint(b)
	switch {
	case n < 0:
		return Record{}, 0, ErrCorrupt // 长度前缀溢出
	case n == 0:
		return Record{}, 0, errIncomplete
	case klen > uint64(len(b)-n):
		return Record{}, 0, errIncomplete
	}
	key := string(b[n : n+int(klen)])
	rest := b[n+int(klen):]
	if len(rest) < 1 {
		return Record{}, 0, errIncomplete
	}
	if rest[0] != TagPut && rest[0] != TagDel {
		return Record{}, 0, ErrCorrupt
	}
	vlen, n2 := binary.Uvarint(rest[1:])
	switch {
	case n2 < 0:
		return Record{}, 0, ErrCorrupt
	case n2 == 0:
		return Record{}, 0, errIncomplete
	case vlen > uint64(len(rest)-1-n2):
		return Record{}, 0, errIncomplete
	}
	val := string(rest[1+n2 : 1+n2+int(vlen)])
	return Record{Key: key, Val: val, Del: rest[0] == TagDel}, n + int(klen) + 1 + n2 + int(vlen), nil
}

// Latest 返回段内该键最后一条记录（段内靠后者更新）。
func (s Segment) Latest(key string) (Record, bool) {
	for i := len(s.Recs) - 1; i >= 0; i-- {
		if s.Recs[i].Key == key {
			return s.Recs[i], true
		}
	}
	return Record{}, false
}
