package constpool

import (
	"math"
)

// naiveModel 是按规则直写的朴素模拟：线性扫描去重，与 Pool 的实现
// （map 索引）完全独立，用于交叉对照。
type naiveModel struct {
	cap    int
	consts []Constant
}

func newNaiveModel(capacity int) *naiveModel {
	return &naiveModel{cap: capacity}
}

// constEqual 实现去重口径：种类不同永不相同；浮点按位比较但所有
// NaN 视为同一个常量；字符串按字节比较。
func constEqual(a, b Constant) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindInt:
		return a.Int == b.Int
	case KindFloat:
		if math.IsNaN(a.Float) && math.IsNaN(b.Float) {
			return true
		}
		return math.Float64bits(a.Float) == math.Float64bits(b.Float)
	case KindString:
		return a.Str == b.Str
	}
	return false
}

func knownKind(c Constant) bool {
	return c.Kind == KindInt || c.Kind == KindFloat || c.Kind == KindString
}

func (m *naiveModel) intern(c Constant) (int, error) {
	if !knownKind(c) {
		return 0, ErrUnknownKind
	}
	for i, existing := range m.consts {
		if constEqual(existing, c) {
			return i, nil
		}
	}
	if len(m.consts) >= m.cap {
		return 0, ErrPoolFull
	}
	m.consts = append(m.consts, normalizeConst(c))
	return len(m.consts) - 1, nil
}

func (m *naiveModel) merge(snapshot []Constant) ([]int, error) {
	for _, c := range snapshot {
		if !knownKind(c) {
			return nil, ErrUnknownKind
		}
	}
	fresh := 0
	for i, c := range snapshot {
		dup := false
		for j := 0; j < i; j++ {
			if constEqual(snapshot[j], c) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		hit := false
		for _, existing := range m.consts {
			if constEqual(existing, c) {
				hit = true
				break
			}
		}
		if !hit {
			fresh++
		}
	}
	if len(m.consts)+fresh > m.cap {
		return nil, ErrMergeExceedsCapacity
	}
	reloc := make([]int, len(snapshot))
	for i, c := range snapshot {
		idx, err := m.intern(c)
		if err != nil {
			return nil, err
		}
		reloc[i] = idx
	}
	return reloc, nil
}

func (m *naiveModel) get(index int) (Constant, error) {
	if index < 0 || index >= len(m.consts) {
		return Constant{}, ErrIndexOutOfRange
	}
	return m.consts[index], nil
}
