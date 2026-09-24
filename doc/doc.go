// Package doc 定义记录集合（键 → 字段映射）及其规范化编码。
package doc

import (
	"encoding/binary"
	"errors"
	"math"
	"sort"
)

// Kind 是字段值的类型标签。
type Kind byte

const (
	KindString Kind = iota
	KindNumber
)

func (k Kind) String() string {
	if k == KindNumber {
		return "number"
	}
	return "string"
}

// Value 是带类型的字段值；Str/Num 为构造函数。
type Value struct {
	Kind Kind
	S    string
	N    float64
}

func Str(s string) Value  { return Value{Kind: KindString, S: s} }
func Num(n float64) Value { return Value{Kind: KindNumber, N: n} }

// Record 是字段名到值的映射；字段不存在与存在但为空串可区分。
type Record map[string]Value

// Set 是键到记录的映射（全量快照）。
type Set map[string]Record

// Equal 比较两个值，类型与内容都参与。RecordEqual 比较两条记录。
func Equal(a, b Value) bool { return a == b }

func RecordEqual(a, b Record) bool {
	if len(a) != len(b) {
		return false
	}
	for f, av := range a {
		bv, ok := b[f]
		if !ok || av != bv {
			return false
		}
	}
	return true
}

// SortedKeys/SortedFields 返回键或字段名的字典序副本。
func SortedKeys(s Set) []string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func SortedFields(r Record) []string {
	fields := make([]string, 0, len(r))
	for f := range r {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	return fields
}

// Canonical 返回集合的规范化编码：键字典序、字段字典序，
// 与构造顺序及 map 迭代顺序无关。空串键/字段名由长度前缀支持。
func Canonical(s Set) []byte {
	var buf []byte
	buf = binary.AppendUvarint(buf, uint64(len(s)))
	for _, k := range SortedKeys(s) {
		buf = appendString(buf, k)
		rec := s[k]
		buf = binary.AppendUvarint(buf, uint64(len(rec)))
		for _, f := range SortedFields(rec) {
			buf = appendString(buf, f)
			buf = appendValue(buf, rec[f])
		}
	}
	return buf
}

func appendString(buf []byte, s string) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(s)))
	return append(buf, s...)
}

func appendValue(buf []byte, v Value) []byte {
	buf = append(buf, byte(v.Kind))
	if v.Kind == KindNumber {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], math.Float64bits(v.N))
		return append(buf, b[:]...)
	}
	return appendString(buf, v.S)
}

// Decode 是 Canonical 的逆过程，供序列化层读回。
func Decode(b []byte) (Set, int, error) {
	d := &decoder{b: b}
	n := d.uvarint()
	s := make(Set, n)
	for i := uint64(0); i < n; i++ {
		k := d.str()
		nf := d.uvarint()
		rec := make(Record, nf)
		for j := uint64(0); j < nf; j++ {
			f := d.str()
			rec[f] = d.value()
		}
		s[k] = rec
	}
	if d.err != nil {
		return nil, 0, d.err
	}
	return s, d.off, nil
}

type decoder struct {
	b   []byte
	off int
	err error
}

// ErrTruncated 表示编码数据被截断。
var ErrTruncated = errors.New("doc: 编码数据被截断")

func (d *decoder) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || len(d.b)-d.off < n {
		d.err = ErrTruncated
		return nil
	}
	p := d.b[d.off : d.off+n]
	d.off += n
	return p
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b[d.off:])
	if n <= 0 {
		d.err = ErrTruncated
		return 0
	}
	d.off += n
	return v
}

func (d *decoder) str() string {
	n := d.uvarint()
	if d.err != nil {
		return ""
	}
	if n > uint64(len(d.b)-d.off) {
		d.err = ErrTruncated
		return ""
	}
	return string(d.take(int(n)))
}

func (d *decoder) value() Value {
	kb := d.take(1)
	if d.err != nil {
		return Value{}
	}
	if Kind(kb[0]) == KindNumber {
		b := d.take(8)
		if d.err != nil {
			return Value{}
		}
		return Num(math.Float64frombits(binary.BigEndian.Uint64(b)))
	}
	return Str(d.str())
}
