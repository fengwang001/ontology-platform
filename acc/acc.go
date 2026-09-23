// Package acc 维护单分组聚合状态（Count/Sum/Min/Max）及部分聚合的合并，
// 并提供溢出片段所用的二进制编解码。
package acc

import (
	"encoding/binary"
	"errors"
	"math"
)

// State 是单个分组键的部分聚合状态。
type State struct {
	Count       int64
	Sum, Min, Max float64
	Has         bool // 区分空状态与真实 ±Inf
}

// ErrState 表示状态行体解码失败。
var ErrState = errors.New("acc: bad state body")

func normZero(v float64) float64 {
	if v == 0 {
		return 0 // ±0.0 视为相等，统一为 +0
	}
	return v
}

// Add 向状态并入一个输入数值。
func (s *State) Add(v float64) {
	v = normZero(v)
	if !s.Has {
		s.Min, s.Max, s.Has = v, v, true
	} else {
		if v < s.Min {
			s.Min = v
		}
		if v > s.Max {
			s.Max = v
		}
	}
	s.Sum = normZero(s.Sum + v)
	s.Count++
}

// Merge 把另一部分状态并入；空状态直接取对方（Min/Max 不做加减）。
func (s *State) Merge(o State) {
	if !o.Has {
		return
	}
	if !s.Has {
		s.Min, s.Max, s.Has = o.Min, o.Max, true
	} else {
		if o.Min < s.Min {
			s.Min = o.Min
		}
		if o.Max > s.Max {
			s.Max = o.Max
		}
	}
	s.Sum = normZero(s.Sum + o.Sum)
	s.Count += o.Count
}

// Keyed 是带分组键的部分状态，用于溢出片段读写。
type Keyed struct {
	Key string
	St  State
}

// Encode: keylen(u32) | key | count(u64) | sum/min/max(u64 each)。
func (k Keyed) Encode() []byte {
	kb := []byte(k.Key)
	buf := make([]byte, 4+len(kb)+8*4)
	binary.BigEndian.PutUint32(buf, uint32(len(kb)))
	copy(buf[4:], kb)
	off := 4 + len(kb)
	binary.BigEndian.PutUint64(buf[off:], uint64(k.St.Count))
	binary.BigEndian.PutUint64(buf[off+8:], math.Float64bits(k.St.Sum))
	binary.BigEndian.PutUint64(buf[off+16:], math.Float64bits(k.St.Min))
	binary.BigEndian.PutUint64(buf[off+24:], math.Float64bits(k.St.Max))
	return buf
}

// DecodeKeyed 解码 Encode 产生的行体。
func DecodeKeyed(buf []byte) (Keyed, error) {
	if len(buf) < 4 {
		return Keyed{}, ErrState
	}
	kl := int(binary.BigEndian.Uint32(buf))
	if kl < 0 || 4+kl+32 != len(buf) {
		return Keyed{}, ErrState
	}
	k := Keyed{Key: string(buf[4 : 4+kl])}
	off := 4 + kl
	k.St.Count = int64(binary.BigEndian.Uint64(buf[off:]))
	k.St.Sum = math.Float64frombits(binary.BigEndian.Uint64(buf[off+8:]))
	k.St.Min = math.Float64frombits(binary.BigEndian.Uint64(buf[off+16:]))
	k.St.Max = math.Float64frombits(binary.BigEndian.Uint64(buf[off+24:]))
	k.St.Has = k.St.Count > 0
	return k, nil
}
