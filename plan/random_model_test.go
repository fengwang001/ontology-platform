package plan

import (
	"fmt"
	"math/rand"

	"ontology/diff"
	"ontology/norm"
)

// naiveRow 为朴素模拟中的一行（源/目标通用）。
type naiveRow struct {
	d *int64
	c []byte // nil=NULL；空 slice=空串
}

// naiveModel 是完全独立于产品代码的逐步朴素模拟（自带归一化与版本逻辑）。
type naiveModel struct {
	sd, rm int
	ne     bool
	src    map[int64]naiveRow
	tgt    map[int64]naiveRow
	ver    map[int64]int64
}

func naiveNew(sd, rm int, ne bool) *naiveModel {
	return &naiveModel{sd: sd, rm: rm, ne: ne,
		src: map[int64]naiveRow{}, tgt: map[int64]naiveRow{}, ver: map[int64]int64{}}
}

func naiveAbs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func (m *naiveModel) cvt(mv int64) int64 {
	d := int64(1)
	for i := 0; i < 6-m.sd; i++ {
		d *= 10
	}
	q := mv / d
	r := mv - q*d
	ar := naiveAbs(r)
	add := false
	switch m.rm {
	case norm.RMHalfUp:
		add = 2*ar >= d
	case norm.RMHalfEven:
		add = 2*ar > d || (2*ar == d && q%2 != 0)
	}
	if add {
		if r > 0 {
			q++
		} else if r < 0 {
			q--
		}
	}
	return q
}

func (m *naiveModel) normC(c []byte) []byte {
	if c == nil {
		return nil
	}
	end := len(c)
	for end > 0 && c[end-1] == 0x20 {
		end--
	}
	if end == 0 {
		if m.ne {
			return nil
		}
		return []byte{}
	}
	return append([]byte(nil), c[:end]...)
}

func nDEq(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func nCEq(a, b []byte) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return string(a) == string(b)
}

type naiveResult struct {
	id   int64
	kind int
	cols []int
	sd   *int64
	sc   []byte
	td   *int64
	tc   []byte
	ver  int64
	ex   bool
}

func (m *naiveModel) compare(lo, hi int64) []naiveResult {
	ids := map[int64]struct{}{}
	for id := range m.src {
		if id >= lo && id < hi {
			ids[id] = struct{}{}
		}
	}
	for id := range m.tgt {
		if id >= lo && id < hi {
			ids[id] = struct{}{}
		}
	}
	sorted := make([]int64, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1] > sorted[j]; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	out := []naiveResult{}
	for _, id := range sorted {
		s, sok := m.src[id]
		t, tok := m.tgt[id]
		r := naiveResult{id: id, ex: tok}
		switch {
		case sok && !tok:
			r.kind = diff.Missing
			if s.d != nil {
				v := m.cvt(*s.d)
				r.sd = &v
			}
			r.sc = m.normC(s.c)
		case !sok && tok:
			r.kind = diff.Extra
			r.td, r.tc, r.ver = t.d, m.normC(t.c), m.ver[id]
		default:
			var sv *int64
			if s.d != nil {
				v := m.cvt(*s.d)
				sv = &v
			}
			sc, tc := m.normC(s.c), m.normC(t.c)
			r.sd, r.sc, r.td, r.tc, r.ver = sv, sc, t.d, tc, m.ver[id]
			if !nDEq(sv, t.d) {
				r.cols = append(r.cols, diff.ColD)
			}
			if !nCEq(sc, tc) {
				r.cols = append(r.cols, diff.ColC)
			}
			if len(r.cols) > 0 {
				r.kind = diff.Changed
			}
		}
		out = append(out, r)
	}
	return out
}

type naiveItem struct {
	id, ver int64
	kind    int
	d       *int64
	c       []byte
	cols    []int
	ex      bool
}

func (m *naiveModel) plan(lo, hi int64, del bool) []naiveItem {
	its := []naiveItem{}
	for _, r := range m.compare(lo, hi) {
		switch r.kind {
		case diff.Missing:
			its = append(its, naiveItem{id: r.id, kind: KindInsert, d: r.sd, c: r.sc,
				cols: []int{diff.ColD, diff.ColC}})
		case diff.Changed:
			its = append(its, naiveItem{id: r.id, kind: KindUpdate, d: r.sd, c: r.sc,
				cols: r.cols, ver: r.ver, ex: true})
		case diff.Extra:
			if del {
				its = append(its, naiveItem{id: r.id, kind: KindDelete, ver: r.ver, ex: true})
			}
		}
	}
	return its
}

// napply 朴素地“先全部校验再提交”，返回 (ins,upd,del,staleID,ok)。
func (m *naiveModel) napply(its []naiveItem) (int, int, int, int64, bool) {
	for _, it := range its {
		_, tok := m.tgt[it.id]
		if tok != it.ex || (tok && m.ver[it.id] != it.ver) {
			return 0, 0, 0, it.id, false
		}
	}
	ins, upd, dn := 0, 0, 0
	for _, it := range its {
		switch it.kind {
		case KindInsert:
			m.tgt[it.id] = naiveRow{d: it.d, c: it.c}
			m.ver[it.id] = 1
			ins++
		case KindUpdate:
			cur := m.tgt[it.id]
			for _, col := range it.cols {
				if col == diff.ColD {
					cur.d = it.d
				} else {
					cur.c = it.c
				}
			}
			m.tgt[it.id] = cur
			m.ver[it.id]++
			upd++
		case KindDelete:
			delete(m.tgt, it.id)
			m.ver[it.id]++
			dn++
		}
	}
	return ins, upd, dn, 0, true
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}

func clonePtr(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func randRow(rng *rand.Rand, id int64) diff.Row {
	r := diff.Row{ID: id}
	switch rng.Intn(3) {
	case 0:
		r.D = nil
	case 1:
		v := rng.Int63n(2*norm.MaxM+1) - norm.MaxM
		r.D = &v
	default:
		// 偏向产生恰半尾数（sd=2 的 D=10000，±5000 为半格）
		v := (rng.Int63n(2000)-1000)*10000 + 5000
		if rng.Intn(2) == 0 {
			v -= 10000
		}
		r.D = &v
	}
	n := rng.Intn(6)
	if n == 0 {
		r.C = nil
	} else {
		buf := make([]byte, n)
		for i := range buf {
			switch rng.Intn(4) {
			case 0:
				buf[i] = 0x20
			case 1:
				buf[i] = 0x09
			default:
				buf[i] = byte('a' + rng.Intn(3))
			}
		}
		r.C = buf
	}
	return r
}

func fmtRow(r diff.Row) string {
	ds := "NULL"
	if r.D != nil {
		ds = fmt.Sprintf("%d", *r.D)
	}
	return fmt.Sprintf("(id=%d d=%s c=%q)", r.ID, ds, r.C)
}
