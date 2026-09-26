package span

import "errors"

var ErrOutOfRange = errors.New("span: offset out of range")

type Edit struct {
	Src, Out   int
	SDel, ODel int
}

type Map struct {
	edits      []Edit
	src, out   int
	lastChecks int
}

func NewMap(src, out int) *Map { return &Map{src: src, out: out} }

func (m *Map) SrcLen() int { return m.src }
func (m *Map) OutLen() int { return m.out }

func (m *Map) Add(e Edit) {
	m.edits = append(m.edits, e)
}

func (m *Map) Edits() []Edit { return append([]Edit(nil), m.edits...) }

func (m *Map) LastChecks() int { return m.lastChecks }

func (m *Map) FoldFrom(src, out, sDel, oDel int) {
	i := 0
	for i < len(m.edits) && m.edits[i].Src < src {
		i++
	}
	m.edits = append(m.edits[:i], Edit{src, out, sDel, oDel})
}

func binarySearch(n int, f func(int) bool) (int, int) {
	checks, lo, hi := 0, 0, n
	for lo < hi {
		mid := int(uint(lo+hi) / 2)
		checks++
		if f(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, checks
}

func (m *Map) ToOrig(o int) (int, error) {
	if o < 0 || o > m.out {
		return 0, ErrOutOfRange
	}
	n := len(m.edits)
	k, checks := binarySearch(n+1, func(k int) bool {
		end := m.out
		if k < n {
			end = m.edits[k].Out
		}
		return o < end
	})
	m.lastChecks = checks
	if k <= 0 {
		return o, nil
	}
	e := m.edits[k-1]
	if o < e.Out+e.SDel {
		return e.Src + o - e.Out, nil
	}
	return o + e.Src - e.Out + e.SDel - e.ODel, nil
}

func (m *Map) ToOut(i int) (int, error) {
	if i < 0 || i > m.src {
		return 0, ErrOutOfRange
	}
	n := len(m.edits)
	k, checks := binarySearch(n, func(k int) bool { return i < m.edits[k].Src })
	m.lastChecks = checks
	if k <= 0 {
		return i, nil
	}
	e := m.edits[k-1]
	if i < e.Src {
		return i, nil
	}
	if i < e.Src+e.SDel {
		return e.Out, nil
	}
	return e.Out + i - e.Src - e.SDel + e.ODel, nil
}

func (m *Map) Translate(srcDelta, outDelta int) {
	for i := range m.edits {
		m.edits[i].Src += srcDelta
		m.edits[i].Out += outDelta
	}
}

func (m *Map) TrimSrc(n int) *Map {
	cp := &Map{src: n, out: n}
	for _, e := range m.edits {
		if e.Src < n {
			cp.Add(e)
		}
	}
	if n >= 0 && n < m.src {
		o, _ := cp.ToOut(n)
		cp.out = o
	}
	return cp
}

func Merge(parts ...*Map) *Map {
	total := &Map{}
	for _, part := range parts {
		part.Translate(total.src, total.out)
		total.edits = append(total.edits, part.edits...)
		total.src += part.src
		total.out += part.out
	}
	return total
}
