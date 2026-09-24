// Package acc 实现分组聚合状态 Count/Sum/Min/Max 及其部分聚合合并。
package acc

import (
	"encoding/binary"
	"math"

	"ontology/row"
)

// State 是单个分组键的部分聚合状态。
type State struct {
	Count int64
	Sum   float64
	Min   float64
	Max   float64
}

// Add 把一个输入值并入当前状态（按到达顺序）。
func (s *State) Add(v float64) {
	if s.Count == 0 {
		s.Min, s.Max = v, v
	} else {
		s.Min = math.Min(s.Min, v)
		s.Max = math.Max(s.Max, v)
	}
	s.Sum += v
	s.Count++
}

// Merge 把另一部分状态并入。空状态为恒等元；Min/Max 取极值，
// Count 相加，Sum 按「另一状态代表后到输入」的顺序累加。
func (s *State) Merge(o State) {
	if o.Count == 0 {
		return
	}
	if s.Count == 0 {
		*s = o
		return
	}
	s.Count += o.Count
	s.Sum += o.Sum
	s.Min = math.Min(s.Min, o.Min)
	s.Max = math.Max(s.Max, o.Max)
}

// Table 是驻留内存的分组聚合表。
type Table map[string]*State

// NewTable 创建空聚合表。
func NewTable() Table { return make(Table) }

// Add 并入一行。
func (t Table) Add(r row.Row) {
	st := t[r.Key]
	if st == nil {
		st = &State{}
		t[r.Key] = st
	}
	st.Add(r.Val)
}

// EncodeEntry 布局：keylen|key | count(uint64) | sum/min/max(3*float64)。
func EncodeEntry(key string, st State) []byte {
	b := row.AppendKey(nil, key)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], uint64(st.Count))
	b = append(b, num[:]...)
	for _, v := range []float64{st.Sum, st.Min, st.Max} {
		binary.BigEndian.PutUint64(num[:], math.Float64bits(v))
		b = append(b, num[:]...)
	}
	return b
}

// DecodeEntry 解析 EncodeEntry 的 payload。
func DecodeEntry(p []byte) (string, State, error) {
	key, rest, err := row.DecodeKey(p)
	if err != nil {
		return "", State{}, err
	}
	if len(rest) < 32 {
		return "", State{}, row.ErrShort
	}
	st := State{Count: int64(binary.BigEndian.Uint64(rest))}
	st.Sum = math.Float64frombits(binary.BigEndian.Uint64(rest[8:]))
	st.Min = math.Float64frombits(binary.BigEndian.Uint64(rest[16:]))
	st.Max = math.Float64frombits(binary.BigEndian.Uint64(rest[24:]))
	return key, st, nil
}
