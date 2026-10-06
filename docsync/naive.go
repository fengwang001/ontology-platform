package docsync

import "sort"

// naiveModel 是独立的朴素参照实现：整篇文本用 UTF-16 码元切片维护，
// 诊断用切片维护并每次变更线性推演。与 treap 实现逻辑完全独立，
// 仅共享类型与基础常量，供随机差分测试对照。
type naiveModel struct {
	version int
	cu      []uint16
	nextSeq int
	alive   map[int]naiveDiag
	order   []int
	dead    []naiveDead
}

type naiveDiag struct {
	start, end int
	severity   Severity
	message    string
	bornVer    int
}

type naiveDead struct {
	seq                int
	failedVer, bornVer int
	severity           Severity
	message            string
	start, end         int
}

func newNaive(initial string) *naiveModel {
	return &naiveModel{cu: encode16(initial), alive: make(map[int]naiveDiag)}
}

func encode16(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(r>>10)+0xD800, uint16(r&0x3FF)+0xDC00)
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

func decode16(cu []uint16) string {
	var runes []rune
	for i := 0; i < len(cu); i++ {
		u := cu[i]
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(cu) && isLowSurrogate(cu[i+1]) {
			r := (rune(u-0xD800) << 10) + rune(cu[i+1]-0xDC00) + 0x10000
			runes = append(runes, r)
			i++
		} else {
			runes = append(runes, rune(u))
		}
	}
	return string(runes)
}

func isLowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

func (m *naiveModel) lineBounds(line int) (start, end int, ok bool) {
	if line < 0 {
		return 0, 0, false
	}
	pos := 0
	for i := 0; i < line; i++ {
		for pos < len(m.cu) && m.cu[pos] != '\n' {
			pos++
		}
		if pos >= len(m.cu) {
			return 0, 0, false
		}
		pos++
	}
	start = pos
	for pos < len(m.cu) && m.cu[pos] != '\n' {
		pos++
	}
	return start, pos, true
}

func (m *naiveModel) positionToOffset(p Position) (int, error) {
	start, end, ok := m.lineBounds(p.Line)
	if !ok {
		return 0, ErrOutOfBounds
	}
	if p.Character < 0 || p.Character > end-start {
		return 0, ErrOutOfBounds
	}
	if p.Character < end-start && isLowSurrogate(m.cu[start+p.Character]) {
		return 0, ErrInsideSurrogatePair
	}
	return start + p.Character, nil
}

func (m *naiveModel) offsetToPosition(off int) (Position, error) {
	if off < 0 || off > len(m.cu) {
		return Position{}, ErrOutOfBounds
	}
	if off < len(m.cu) && isLowSurrogate(m.cu[off]) {
		return Position{}, ErrInsideSurrogatePair
	}
	line, lineStart := 0, 0
	for i := 0; i < off; i++ {
		if m.cu[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	return Position{Line: line, Character: off - lineStart}, nil
}

type naiveEdit struct {
	s, e int
	text string
	tl   int
	orig int
}

type naiveGroup struct {
	s, e     int
	tl       int
	nonEmpty bool
}

func groupNaiveEdits(edits []naiveEdit) []naiveGroup {
	var gs []naiveGroup
	for _, ed := range edits {
		if n := len(gs); n > 0 && ed.e == ed.s && !gs[n-1].nonEmpty && gs[n-1].s == ed.s {
			gs[n-1].tl += ed.tl
			continue
		}
		gs = append(gs, naiveGroup{s: ed.s, e: ed.e, tl: ed.tl, nonEmpty: ed.e > ed.s})
	}
	return gs
}

func (m *naiveModel) change(baseVersion int, edits []Edit) ChangeResult {
	res := ChangeResult{BeforeVersion: m.version, AfterVersion: m.version}
	if baseVersion != m.version {
		res.Reason = ErrStaleVersion
		return res
	}
	planned := make([]naiveEdit, 0, len(edits))
	for i, ed := range edits {
		st, err := m.positionToOffset(ed.Range.Start)
		if err != nil {
			res.Reason = err
			return res
		}
		en, err := m.positionToOffset(ed.Range.End)
		if err != nil {
			res.Reason = err
			return res
		}
		if st > en {
			res.Reason = ErrInvalidRange
			return res
		}
		planned = append(planned, naiveEdit{s: st, e: en, text: ed.Text, tl: len(encode16(ed.Text)), orig: i})
	}
	sort.SliceStable(planned, func(i, j int) bool {
		if planned[i].s != planned[j].s {
			return planned[i].s < planned[j].s
		}
		ie, je := planned[i].e == planned[i].s, planned[j].e == planned[j].s
		if ie != je {
			return ie
		}
		return planned[i].orig < planned[j].orig
	})
	for i := 1; i < len(planned); i++ {
		if planned[i-1].e > planned[i-1].s && planned[i-1].e > planned[i].s {
			res.Reason = ErrOverlappingEdits
			return res
		}
	}

	afterVersion := m.version + 1
	deadSet := make(map[int]struct{})
	var deadSeq []int
	for _, ed := range planned {
		if ed.e == ed.s {
			continue
		}
		for _, seq := range m.order {
			if _, gone := deadSet[seq]; gone {
				continue
			}
			d := m.alive[seq]
			if d.start < d.end {
				if d.start < ed.e && ed.s < d.end {
					deadSet[seq] = struct{}{}
					deadSeq = append(deadSeq, seq)
				}
			} else if d.start > ed.s && d.start < ed.e {
				deadSet[seq] = struct{}{}
				deadSeq = append(deadSeq, seq)
			}
		}
	}
	sort.Ints(deadSeq)
	for _, seq := range deadSeq {
		d := m.alive[seq]
		m.dead = append(m.dead, naiveDead{
			seq: seq, failedVer: afterVersion, bornVer: d.bornVer,
			severity: d.severity, message: d.message, start: d.start, end: d.end,
		})
		delete(m.alive, seq)
	}

	groups := groupNaiveEdits(planned)
	mapPoint := func(p int, isStart bool) int {
		out := p
		for _, g := range groups {
			if g.nonEmpty {
				if p > g.s && p >= g.e {
					out += g.tl - (g.e - g.s)
				}
			} else if p >= g.s {
				if p == g.s && !isStart {
					// 终点恰在插入点：非空诊断 b==s>a 不吃；空诊断由调用端按 start 统一。
				} else {
					out += g.tl
				}
			}
		}
		return out
	}
	newAlive := make(map[int]naiveDiag, len(m.alive))
	newOrder := make([]int, 0, len(m.alive))
	for _, seq := range m.order {
		d, ok := m.alive[seq]
		if !ok {
			continue
		}
		ns := mapPoint(d.start, true)
		ne := ns
		if d.start != d.end {
			ne = mapPoint(d.end, false)
		}
		d.start, d.end = ns, ne
		newAlive[seq] = d
		newOrder = append(newOrder, seq)
	}
	m.alive = newAlive
	m.order = newOrder

	out := m.cu
	for i := len(planned) - 1; i >= 0; i-- {
		ed := planned[i]
		ins := encode16(ed.text)
		next := make([]uint16, 0, len(out)-(ed.e-ed.s)+len(ins))
		next = append(next, out[:ed.s]...)
		next = append(next, ins...)
		next = append(next, out[ed.e:]...)
		out = next
	}
	m.cu = out
	m.version = afterVersion

	res.Accepted = true
	res.AfterVersion = afterVersion
	res.Dead = deadSeq
	return res
}

func (m *naiveModel) register(baseVersion int, d Diagnostic) (int, error) {
	if baseVersion != m.version {
		return 0, ErrStaleVersion
	}
	if d.Range.Start.Line != d.Range.End.Line {
		if d.Range.Start.Line > d.Range.End.Line {
			return 0, ErrInvalidRange
		}
	} else if d.Range.Start.Character > d.Range.End.Character {
		return 0, ErrInvalidRange
	}
	st, err := m.positionToOffset(d.Range.Start)
	if err != nil {
		return 0, err
	}
	en, err := m.positionToOffset(d.Range.End)
	if err != nil {
		return 0, err
	}
	m.nextSeq++
	m.alive[m.nextSeq] = naiveDiag{
		start: st, end: en, severity: d.Severity, message: d.Message, bornVer: m.version,
	}
	m.order = append(m.order, m.nextSeq)
	return m.nextSeq, nil
}

// activeDiags 返回按 (start,end,seq) 排序的有效诊断（偏移形式，供差分）。
type naiveItem struct {
	seq     int
	start   int
	end     int
	message string
}

func (m *naiveModel) activeDiags() []naiveItem {
	var items []naiveItem
	for _, seq := range m.order {
		d := m.alive[seq]
		items = append(items, naiveItem{seq: seq, start: d.start, end: d.end, message: d.message})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].start != items[j].start {
			return items[i].start < items[j].start
		}
		if items[i].end != items[j].end {
			return items[i].end < items[j].end
		}
		return items[i].seq < items[j].seq
	})
	return items
}

// deadMeta 返回 (seq, bornVer, failedVer) 的失效清单顺序。
func (m *naiveModel) deadMeta() [][3]int {
	out := make([][3]int, 0, len(m.dead))
	for _, d := range m.dead {
		out = append(out, [3]int{d.seq, d.bornVer, d.failedVer})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i][2] != out[j][2] {
			return out[i][2] < out[j][2]
		}
		return out[i][0] < out[j][0]
	})
	return out
}
