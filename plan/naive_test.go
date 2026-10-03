package plan_test

// 朴素模拟器：独立于 norm/diff/plan，用 map 与逐行规则重新实现，用于随机对拍。

type simRow struct {
	hasD bool
	d    int64
	hasC bool // 区分 NULL 与空串
	c    []byte
}

type naiveSim struct {
	sd, rm int
	ne     bool
	src    map[int64]simRow
	tgt    map[int64]simRow
	ver    map[int64]int64
}

func newNaive(sd, rm int, ne bool) *naiveSim {
	return &naiveSim{
		sd:  sd,
		rm:  rm,
		ne:  ne,
		src: map[int64]simRow{},
		tgt: map[int64]simRow{},
		ver: map[int64]int64{},
	}
}

func (s *naiveSim) divScale() int64 {
	v := int64(1)
	for i := 0; i < 6-s.sd; i++ {
		v *= 10
	}
	return v
}

// cvtD 严格按题面：朝零商 q、余数 r，三种舍入。
func (s *naiveSim) cvtD(m int64) int64 {
	D := s.divScale()
	q := m / D
	r := m - q*D
	if r == 0 || s.rm == 2 {
		return q
	}
	sign := int64(1)
	if m < 0 {
		sign = -1
	}
	ar := r
	if ar < 0 {
		ar = -ar
	}
	two := 2 * ar
	if s.rm == 0 && two >= D {
		q += sign
	}
	if s.rm == 1 && (two > D || (two == D && q%2 != 0)) {
		q += sign
	}
	return q
}

func (s *naiveSim) normC(b []byte, hasC bool) ([]byte, bool) {
	if !hasC {
		return nil, false
	}
	n := len(b)
	for n > 0 && b[n-1] == 0x20 {
		n--
	}
	out := append([]byte(nil), b[:n]...)
	if n == 0 && s.ne {
		return nil, false
	}
	return out, true
}

func (s *naiveSim) normRow(r simRow, source bool) simRow {
	o := simRow{}
	if r.hasD {
		o.hasD = true
		if source {
			o.d = s.cvtD(r.d)
		} else {
			o.d = r.d
		}
	}
	o.c, o.hasC = s.normC(r.c, r.hasC)
	return o
}

func (s *naiveSim) srcPut(id int64, hasD bool, d int64, hasC bool, c []byte) {
	s.src[id] = simRow{hasD: hasD, d: d, hasC: hasC, c: append([]byte(nil), c...)}
}

func (s *naiveSim) tgtPut(id int64, hasD bool, d int64, hasC bool, c []byte) {
	s.tgt[id] = simRow{hasD: hasD, d: d, hasC: hasC, c: append([]byte(nil), c...)}
	s.ver[id]++
}

func (s *naiveSim) tgtDel(id int64) {
	delete(s.tgt, id)
	s.ver[id]++
}

type simResult struct {
	id    int64
	cls   int // 0 missing 1 extra 2 equal 3 changed
	dDiff bool
	cDiff bool
}

func sortedIDs(m map[int64]simRow, lo, hi int64) []int64 {
	var ids []int64
	for id := range m {
		if id >= lo && id < hi {
			ids = append(ids, id)
		}
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	return ids
}

func dEq(a, b simRow) bool {
	if a.hasD != b.hasD {
		return false
	}
	return !a.hasD || a.d == b.d
}

func cEq(a, b simRow) bool {
	if a.hasC != b.hasC {
		return false
	}
	if !a.hasC {
		return true
	}
	if len(a.c) != len(b.c) {
		return false
	}
	for i := range a.c {
		if a.c[i] != b.c[i] {
			return false
		}
	}
	return true
}

func (s *naiveSim) compare(lo, hi int64) []simResult {
	sids := sortedIDs(s.src, lo, hi)
	tids := sortedIDs(s.tgt, lo, hi)
	var out []simResult
	i, j := 0, 0
	for i < len(sids) || j < len(tids) {
		switch {
		case j == len(tids) || (i < len(sids) && sids[i] < tids[j]):
			out = append(out, simResult{id: sids[i], cls: 0})
			i++
		case i == len(sids) || (j < len(tids) && tids[j] < sids[i]):
			out = append(out, simResult{id: tids[j], cls: 1})
			j++
		default:
			id := sids[i]
			sr := s.normRow(s.src[id], true)
			gr := s.normRow(s.tgt[id], false)
			de, ce := !dEq(sr, gr), !cEq(sr, gr)
			cls := 2
			if de || ce {
				cls = 3
			}
			out = append(out, simResult{id: id, cls: cls, dDiff: de, cDiff: ce})
			i++
			j++
		}
	}
	return out
}

type simOp struct {
	kind         int // 0 insert 1 update 2 delete
	id           int64
	d            simRow
	maskD, maskC bool
	version      int64
}

func (s *naiveSim) makePlan(lo, hi int64, del bool) []simOp {
	rs := s.compare(lo, hi)
	var ops []simOp
	for _, r := range rs {
		switch r.cls {
		case 0:
			sr := s.normRow(s.src[r.id], true)
			ops = append(ops, simOp{kind: 0, id: r.id, d: sr, version: 0})
		case 3:
			sr := s.normRow(s.src[r.id], true)
			op := simOp{kind: 1, id: r.id, d: sr, version: s.ver[r.id],
				maskD: r.dDiff, maskC: r.cDiff}
			ops = append(ops, op)
		case 1:
			if del {
				ops = append(ops, simOp{kind: 2, id: r.id, version: s.ver[r.id]})
			}
		}
	}
	return ops
}

// apply 返回 (ni,nu,nd,staleID,ok,permissionDenied)。整体原子。
func (s *naiveSim) apply(role int, ops []simOp) (int, int, int, int64, bool, bool) {
	hasDel := false
	for _, op := range ops {
		if op.kind == 2 {
			hasDel = true
		}
	}
	if role != 1 && role != 2 {
		return 0, 0, 0, 0, false, true
	}
	if hasDel && role != 2 {
		return 0, 0, 0, 0, false, true
	}
	if len(ops) == 0 {
		return 0, 0, 0, 0, true, false
	}
	staleID := int64(0)
	found := false
	for _, op := range ops {
		_, present := s.tgt[op.id]
		bad := false
		if op.kind == 0 {
			bad = present
		} else {
			bad = !present || s.ver[op.id] != op.version
		}
		if bad && (!found || op.id < staleID) {
			staleID, found = op.id, true
		}
	}
	if found {
		return 0, 0, 0, staleID, false, false
	}
	ni, nu, nd := 0, 0, 0
	for _, op := range ops {
		switch op.kind {
		case 0:
			s.tgt[op.id] = simRow{hasD: op.d.hasD, d: op.d.d,
				hasC: op.d.hasC, c: append([]byte(nil), op.d.c...)}
			s.ver[op.id]++
			ni++
		case 1:
			cur := s.tgt[op.id]
			if op.maskD {
				cur.hasD, cur.d = op.d.hasD, op.d.d
			}
			if op.maskC {
				cur.hasC, cur.c = op.d.hasC, append([]byte(nil), op.d.c...)
			}
			s.tgt[op.id] = cur
			s.ver[op.id]++
			nu++
		case 2:
			delete(s.tgt, op.id)
			s.ver[op.id]++
			nd++
		}
	}
	return ni, nu, nd, 0, true, false
}
