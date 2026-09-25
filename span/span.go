package span

import "sort"

type Seg struct {
	Orig0, Orig1 int
	Out0, Out1   int
}

type Mapper struct {
	segs    []Seg
	checked int
}

func (m *Mapper) Len() int { return len(m.segs) }

func (m *Mapper) Checked() int { return m.checked }

func (m *Mapper) End() (orig, out int) {
	if len(m.segs) == 0 {
		return 0, 0
	}
	s := m.segs[len(m.segs)-1]
	return s.Orig1, s.Out1
}

func (m *Mapper) Add(s Seg) {
	if n := len(m.segs); n > 0 {
		p := m.segs[n-1]
		deleted := p.Out0 == p.Out1 && s.Out0 == s.Out1
		adjacent := p.Orig1 == s.Orig0 && p.Out1 == s.Out0
		if adjacent && (p.Out1 == s.Out0 && (s.Out0 != s.Out1 || deleted)) {
			m.segs[n-1].Orig1 = s.Orig1
			m.segs[n-1].Out1 = s.Out1
			return
		}
	}
	m.segs = append(m.segs, s)
}

func (m *Mapper) Truncate(origEnd int) {
	idx := sort.Search(len(m.segs), func(i int) bool { return m.segs[i].Orig1 >= origEnd })
	if idx == len(m.segs) {
		return
	}
	s := m.segs[idx]
	if s.Orig0 < origEnd {
		cut := s.Out0
		if s.Out1 > s.Out0 {
			cut += origEnd - s.Orig0
		}
		s.Orig1, s.Out1 = origEnd, cut
		m.segs[idx] = s
		idx++
	}
	m.segs = m.segs[:idx]
}

func (m *Mapper) ToOrig(out int) int {
	if len(m.segs) == 0 {
		m.checked = 1
		return 0
	}
	last := m.segs[len(m.segs)-1]
	if out == last.Out1 {
		m.checked = 1
		return last.Orig1
	}
	idx := sort.Search(len(m.segs), func(i int) bool {
		m.checked++
		return m.segs[i].Out1 > out
	})
	for m.segs[idx].Out0 == m.segs[idx].Out1 {
		idx++
		m.checked++
	}
	s := m.segs[idx]
	return s.Orig0 + out - s.Out0
}

func (m *Mapper) ToOut(orig int) int {
	if len(m.segs) == 0 {
		m.checked = 1
		return 0
	}
	last := m.segs[len(m.segs)-1]
	if orig == last.Orig1 {
		m.checked = 1
		return last.Out1
	}
	idx := sort.Search(len(m.segs), func(i int) bool {
		m.checked++
		return m.segs[i].Orig1 > orig
	})
	s := m.segs[idx]
	if s.Out1 == s.Out0 {
		return s.Out0
	}
	return s.Out0 + orig - s.Orig0
}

func (m *Mapper) TranslateAppend(in *Mapper, do, dto int) {
	for _, s := range in.segs {
		m.Add(Seg{s.Orig0 + do, s.Orig1 + do, s.Out0 + dto, s.Out1 + dto})
	}
}
