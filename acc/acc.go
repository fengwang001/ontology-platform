// Package acc 定义分组聚合状态（Count、Sum、Min、Max）及其合并与编解码。
package acc

import (
	"encoding/binary"
	"errors"
	"math"
)

// State 是某分组键的部分聚合状态，可与其他部分状态合并。
type State struct {
	Count uint64
	Sum   float64
	Min   float64
	Max   float64
}

// Add 把一个数值并入状态。
func (s *State) Add(v float64) {
	if s.Count == 0 {
		s.Min, s.Max = v, v
	} else {
		s.Min = math.Min(s.Min, v)
		s.Max = math.Max(s.Max, v)
	}
	s.Count++
	s.Sum += v
}

// Merge 把另一个部分状态并入 s；o 为空状态（Count==0）时 s 不变。
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

// Group 是一组输出结果（分组键 + 聚合状态）。
type Group struct {
	Key   string
	State State
}

// stateLen 是状态定长部分的字节数：Count/Sum/Min/Max 各 8 字节。
const stateLen = 32

// ErrDecode 是状态解码失败的哨兵错误。
var ErrDecode = errors.New("acc: 状态解码失败")

// Encode 把 (键, 状态) 编码为：uvarint(键长) | 键字节 | Count | Sum | Min | Max。
func Encode(key string, s State) []byte {
	buf := make([]byte, 0, binary.MaxVarintLen64+len(key)+stateLen)
	buf = binary.AppendUvarint(buf, uint64(len(key)))
	buf = append(buf, key...)
	var tmp [stateLen]byte
	binary.LittleEndian.PutUint64(tmp[0:], s.Count)
	binary.LittleEndian.PutUint64(tmp[8:], math.Float64bits(s.Sum))
	binary.LittleEndian.PutUint64(tmp[16:], math.Float64bits(s.Min))
	binary.LittleEndian.PutUint64(tmp[24:], math.Float64bits(s.Max))
	return append(buf, tmp[:]...)
}

// Decode 解码 Encode 的产物，输入长度必须恰好为一条记录。
func Decode(b []byte) (string, State, error) {
	keyLen, n := binary.Uvarint(b)
	if n <= 0 || uint64(len(b)) != uint64(n)+keyLen+stateLen {
		return "", State{}, ErrDecode
	}
	key := string(b[n : uint64(n)+keyLen])
	rest := b[uint64(n)+keyLen:]
	s := State{
		Count: binary.LittleEndian.Uint64(rest[0:]),
		Sum:   math.Float64frombits(binary.LittleEndian.Uint64(rest[8:])),
		Min:   math.Float64frombits(binary.LittleEndian.Uint64(rest[16:])),
		Max:   math.Float64frombits(binary.LittleEndian.Uint64(rest[24:])),
	}
	return key, s, nil
}
