package bitemporal

import "sort"

// 物理半方向。
const (
	halfAB = "ab"
	halfBA = "ba"
	halfF  = "f"
	halfR  = "r"
)

// halfCode 编码“对象对 + 物理半”：偶数 code 为正向半，code^1 为另一半。
type halfCode struct {
	a, b ID
	side string
}

// halfStream 是同一对象对同一物理半的有序事实流。
type halfStream struct {
	vt          []int64
	rt          []int64
	kind        []int
	suffixMinRT []int64
	first       []int
	// byPrefixEnd[k]：第 k 个 vt 列候选前缀内按 (rt,pos) 升序的位置序列。
	byPrefixEnd [][]int
	// prefixMaxPos[k]：与 byPrefixEnd[k] 对齐的“截至该项的最大位置”前缀。
	prefixMaxPos [][]int
}

type edgeStreams struct {
	a, b ID
	ab   *halfStream
	ba   *halfStream
	// 该半所有获胜段矩形（含死亡段），供对角线求交。
	abRects []rect
	baRects []rect
}

type diagIvl struct {
	lo, hi int64
}

type eraIndex struct {
	ruleStart int64
	ruleEnd   int64
	fwd       *intervalIndex
	rev       *intervalIndex
	fwdObjs   []ID
	revObjs   []ID
	// evFwd/evRev：按对象聚合、按 tick 净变化的度事件。
	evFwd map[ID][]degTick
	evRev map[ID][]degTick
}

type degTick struct {
	at    int64
	delta int
}

type linkIndex struct {
	lt      LinkType
	codes   []halfCode
	edges   map[pairKey]*edgeStreams
	rect    *rectIndex
	defects *intervalIndex
	eras    []*eraIndex
	// outCodes：对象在正向/反向视角下涉及的偶数 halfCode 列表。
	outFwd map[ID][]int
	outRev map[ID][]int
}

// ProbeCounts 记录一次查询过程中对索引结构的访问次数。
type ProbeCounts struct {
	FactScans int
	RuleScans int
	EdgeLooks int
}

type probeKeyType struct{}

var probeKey = probeKeyType{}

func halfName(symmetric bool, token string) string {
	if symmetric {
		if token == halfBA {
			return halfBA
		}
		return halfAB
	}
	if token == halfR {
		return halfR
	}
	return halfF
}

func otherHalf(h string) string {
	switch h {
	case halfAB:
		return halfBA
	case halfBA:
		return halfAB
	case halfF:
		return halfR
	default:
		return halfF
	}
}

func rebuildIndexes(s *Snapshot) map[ID]*linkIndex {
	out := make(map[ID]*linkIndex, len(s.types))
	for id, tk := range s.types {
		out[id] = buildLinkIndex(tk, s.facts[id])
	}
	return out
}

func buildLinkIndex(tk *typeKey, facts []linkFact) *linkIndex {
	idx := &linkIndex{lt: tk.lt, edges: map[pairKey]*edgeStreams{}}

	type mk struct {
		pair pairKey
		side string
	}
	grouped := map[mk][]linkFact{}
	for _, f := range facts {
		key := pairKey{a: f.src, b: f.dst}
		es := idx.edges[key]
		if es == nil {
			es = &edgeStreams{a: f.src, b: f.dst}
			idx.edges[key] = es
		}
		h := halfName(f.symmetric, f.token)
		canonical := f.origin == "c"
		// 反向半事实同样以规范对象对登记（a->b 的 f 方向与 b->a 的 r 方向是同一条边）。
		if !f.symmetric && h == halfR {
			// key 已经是 (src,dst)：IngestLegacyHalf 的 "r" 侧把 a,b 作为
			// (源对象, 目标对象) 原样存储，因此 key 正确。
		}
		if canonical {
			if f.symmetric {
				h = halfAB
			} else {
				h = halfF
			}
		}
		grouped[mk{key, h}] = append(grouped[mk{key, h}], f)
		if canonical {
			oh := otherHalf(h)
			grouped[mk{key, oh}] = append(grouped[mk{key, oh}], f)
		}
	}

	var pairs []pairKey
	for p := range idx.edges {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].a != pairs[j].a {
			return pairs[i].a < pairs[j].a
		}
		return pairs[i].b < pairs[j].b
	})

	posSide, negSide := halfAB, halfBA
	if !tk.lt.Symmetric {
		posSide, negSide = halfF, halfR
	}
	idx.codes = make([]halfCode, 2*len(pairs))
	var rects []rect
	diag := make([][]diagIvl, 2*len(pairs))

	for pi, key := range pairs {
		pos, neg := 2*pi, 2*pi+1
		es := idx.edges[key]
		idx.codes[pos] = halfCode{a: key.a, b: key.b, side: posSide}
		idx.codes[neg] = halfCode{a: key.b, b: key.a, side: negSide}

		attach := func(code int, side string, dst **halfStream, drect *[]rect) {
			fs := grouped[mk{key, side}]
			hs := buildHalfStream(fs)
			*dst = hs
			hr := halfRects(hs, code)
			*drect = hr
			rects = append(rects, hr...)
			diag[code] = rectsDiagonal(hr)
		}
		attach(pos, posSide, &es.ab, &es.abRects)
		attach(neg, negSide, &es.ba, &es.baRects)
	}

	rtTickSet := map[int64]struct{}{}
	for _, r := range rects {
		rtTickSet[r.rt0] = struct{}{}
		if r.rt1 != vtInf {
			rtTickSet[r.rt1] = struct{}{}
		}
	}
	rtTickSet[vtInf] = struct{}{}
	var rtTicks []int64
	for t := range rtTickSet {
		rtTicks = append(rtTicks, t)
	}
	sort.Slice(rtTicks, func(i, j int) bool { return rtTicks[i] < rtTicks[j] })
	idx.rect = newRectIndex(rtTicks, rects, nil)

	var defectIvls []ivl
	for pi := range pairs {
		defectIvls = append(defectIvls, pairDefectsRects(2*pi,
			idx.edges[pairs[pi]].abRects, idx.edges[pairs[pi]].baRects)...)
	}
	idx.defects = newIntervalIndex(defectIvls)

	idx.eras = buildEras(idx.lt, tk.rules, diag, idx.codes)
	idx.outFwd = map[ID][]int{}
	idx.outRev = map[ID][]int{}
	for code := 0; code+1 < len(idx.codes); code += 2 {
		idx.outFwd[idx.codes[code].a] = append(idx.outFwd[idx.codes[code].a], code)
		idx.outRev[idx.codes[code+1].a] = append(idx.outRev[idx.codes[code+1].a], code)
	}
	return idx
}

func buildHalfStream(fs []linkFact) *halfStream {
	if len(fs) == 0 {
		return nil
	}
	sorted := append([]linkFact(nil), fs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].validTime != sorted[j].validTime {
			return sorted[i].validTime < sorted[j].validTime
		}
		if sorted[i].recordTime != sorted[j].recordTime {
			return sorted[i].recordTime < sorted[j].recordTime
		}
		return sorted[i].kind < sorted[j].kind // vt 与 rt 都相同：撤销(kind=2)位置更大，平局获胜
	})
	hs := &halfStream{
		vt:           make([]int64, len(sorted)),
		rt:           make([]int64, len(sorted)),
		kind:         make([]int, len(sorted)),
		suffixMinRT:  make([]int64, len(sorted)),
		byPrefixEnd:  [][]int{},
		prefixMaxPos: [][]int{},
	}
	for i, f := range sorted {
		hs.vt[i] = f.validTime
		hs.rt[i] = f.recordTime
		hs.kind[i] = f.kind
	}
	minRT := int64(-1)
	for i := len(sorted) - 1; i >= 0; i-- {
		if minRT == -1 || sorted[i].recordTime < minRT {
			minRT = sorted[i].recordTime
		}
		hs.suffixMinRT[i] = minRT
	}
	for i := range sorted {
		if i == 0 || hs.vt[i] != hs.vt[i-1] {
			hs.first = append(hs.first, i)
		}
	}
	// 为每个 vt 列构建其候选前缀 [0, end) 的 (rt, pos) 升序表；
	// 后一列的表由前一列与新 vt 段归并得到（每段内部本身按 rt 升序）。
	hs.byPrefixEnd = make([][]int, len(hs.first))
	hs.prefixMaxPos = make([][]int, len(hs.first))
	var prevOrd []int
	for k, start := range hs.first {
		end := len(sorted)
		if k+1 < len(hs.first) {
			end = hs.first[k+1]
		}
		run := make([]int, 0, end-start)
		for p := start; p < end; p++ {
			run = append(run, p)
		}
		sort.Slice(run, func(i, j int) bool {
			if hs.rt[run[i]] != hs.rt[run[j]] {
				return hs.rt[run[i]] < hs.rt[run[j]]
			}
			return run[i] < run[j]
		})
		pos := mergePos(prevOrd, run, func(x, y int) bool {
			if hs.rt[x] != hs.rt[y] {
				return hs.rt[x] < hs.rt[y]
			}
			return x < y
		})
		hs.byPrefixEnd[k] = pos
		maxPos := make([]int, len(pos))
		mx := -1
		for i, p := range pos {
			if p > mx {
				mx = p
			}
			maxPos[i] = mx
		}
		hs.prefixMaxPos[k] = maxPos
		prevOrd = pos
	}
	return hs
}

func mergePos(a, b []int, less func(x, y int) bool) []int {
	out := make([]int, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if less(a[i], b[j]) {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

// halfRects 为每个 vt 获胜段产出一个矩形；alive 标明该段获胜事实是创建还是撤销。
func halfRects(hs *halfStream, code int) []rect {
	if hs == nil {
		return nil
	}
	rtSet := map[int64]struct{}{}
	for _, r := range hs.rt {
		rtSet[r] = struct{}{}
	}
	var rts []int64
	for r := range rtSet {
		rts = append(rts, r)
	}
	sort.Slice(rts, func(i, j int) bool { return rts[i] < rts[j] })

	var out []rect
	for k, fi := range hs.first {
		v0 := hs.vt[fi]
		v1 := int64(vtInf)
		if k+1 < len(hs.first) {
			v1 = hs.vt[hs.first[k+1]]
		}
		prevWin := -2
		var rtLo int64
		prevAlive := false
		flush := func(rtHi int64, win int) {
			if win < 0 || rtLo >= rtHi {
				return
			}
			out = append(out, rect{
				rt0: rtLo, rt1: rtHi, vt0: v0, vt1: v1,
				alive: prevAlive, val: code, order: win,
			})
		}
		for _, r := range rts {
			// 获胜者 = 列前缀内全顺序最大（位置最大）且 rt<=r 的事实。
			// byPrefixEnd[k] 按 (rt,pos) 升序：先数 rt<=r 的个数 cnt，
			// 再在这 cnt 个位置中取位置最大者。
			ord := hs.byPrefixEnd[k]
			cnt := sort.Search(len(ord), func(i int) bool { return hs.rt[ord[i]] > r })
			win := -1
			if cnt > 0 {
				win = hs.prefixMaxPos[k][cnt-1]
			}
			if win != prevWin {
				if prevWin != -2 {
					flush(r, prevWin)
				}
				rtLo = r
				prevWin = win
				prevAlive = win >= 0 && hs.kind[win] == kindCreate
			}
		}
		flush(vtInf, prevWin)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].rt0 != out[j].rt0 {
			return out[i].rt0 < out[j].rt0
		}
		return out[i].vt0 < out[j].vt0
	})
	return out
}

func recordAt(hs *halfStream, p int) int64 { return hs.rt[p] }

// winnerAt 返回 (R, V) 处获胜事实的种类（kindCreate/kindRevoke），无事实时返回 0。
func winnerAt(hs *halfStream, R, V int64) int {
	// 全顺序最大的、满足 vt<=V 且 rt<=R 的事实。
	// 按 vt 升序、同 vt 撤销优先（与 halfStream 排序一致），从后向前找。
	for i := len(hs.vt) - 1; i >= 0; i-- {
		if hs.vt[i] > V {
			continue
		}
		if hs.rt[i] <= R {
			return hs.kind[i]
		}
	}
	return 0
}

func halfDiagonal(hs *halfStream) []diagIvl {
	if hs == nil {
		return nil
	}
	var out []diagIvl
	for k, i := range hs.first {
		rtLo := hs.suffixMinRT[i]
		end := len(hs.vt)
		if k+1 < len(hs.first) {
			end = hs.first[k+1]
		}
		rtHi := int64(vtInf)
		for j := i; j < end; j++ {
			cand := hs.suffixMinRT[j]
			if cand > rtLo && cand < rtHi {
				rtHi = cand
			}
		}
		vtLo := hs.vt[i]
		vtHi := int64(vtInf)
		if k+1 < len(hs.first) {
			vtHi = hs.vt[hs.first[k+1]]
		}
		lo := max64(rtLo, vtLo)
		hi := min64(rtHi, vtHi)
		if lo < hi && hs.kind[i] == kindCreate {
			out = append(out, diagIvl{lo: lo, hi: hi})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].lo < out[j].lo })
	return out
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// pairDefects 求同一对象对两半对角线存活区间的对称差，
// val 记录“存活而另一半缺失”一侧的 halfCode。
func pairDefects(base int, pos, neg []diagIvl) []ivl {
	var points []int64
	for _, in := range pos {
		points = append(points, in.lo, in.hi)
	}
	for _, in := range neg {
		points = append(points, in.lo, in.hi)
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })

	var out []ivl
	alivePos, aliveNeg := false, false
	prev := int64(-1)
	for _, t := range points {
		if prev != -1 && alivePos != aliveNeg {
			missing := base
			if alivePos {
				missing = base + 1
			}
			out = append(out, ivl{lo: prev, hi: t, val: missing})
		}
		alivePos = toggleAt(pos, t, alivePos)
		aliveNeg = toggleAt(neg, t, aliveNeg)
		prev = t
	}
	return out
}

func toggleAt(ivls []diagIvl, t int64, cur bool) bool {
	for _, in := range ivls {
		if in.lo == t {
			cur = true
		}
		if in.hi == t {
			cur = false
		}
	}
	return cur
}

// rectDiagonal 返回单个矩形与对角线 vt==rt 的交（rt 区间）。
func rectDiagonal(r rect) diagIvl {
	lo := max64(r.rt0, r.vt0)
	hi := min64(r.rt1, r.vt1)
	return diagIvl{lo: lo, hi: hi}
}

// rectsDiagonal 求一组（同半）矩形与对角线 vt==rt 的存活交集。
// 多个矩形可能在对角线上相邻或重叠，统一收集后合并。
func rectsDiagonal(rs []rect) []diagIvl {
	var raw []diagIvl
	for _, r := range rs {
		if !r.alive {
			continue
		}
		in := rectDiagonal(r)
		if in.lo < in.hi {
			raw = append(raw, in)
		}
	}
	return mergeDiagIntervals(raw)
}

func mergeDiagIntervals(in []diagIvl) []diagIvl {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].lo < in[j].lo })
	out := in[:1]
	for i := 1; i < len(in); i++ {
		if in[i].lo <= out[len(out)-1].hi {
			if in[i].hi > out[len(out)-1].hi {
				out[len(out)-1].hi = in[i].hi
			}
			continue
		}
		out = append(out, in[i])
	}
	return out
}

// pairDefectsRects 直接由两半矩形求对角线上的存活区间对称差。
func pairDefectsRects(base int, posRects, negRects []rect) []ivl {
	collect := func(rs []rect) []diagIvl {
		var out []diagIvl
		for _, r := range rs {
			if !r.alive {
				continue
			}
			in := rectDiagonal(r)
			if in.lo < in.hi {
				out = append(out, in)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].lo < out[j].lo })
		return out
	}
	return pairDefects(base, collect(posRects), collect(negRects))
}

func intersectIntervals(a, b []diagIvl) []diagIvl {
	var out []diagIvl
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		lo := max64(a[i].lo, b[j].lo)
		hi := min64(a[i].hi, b[j].hi)
		if lo < hi {
			out = append(out, diagIvl{lo: lo, hi: hi})
		}
		if a[i].hi < b[j].hi {
			i++
		} else {
			j++
		}
	}
	return out
}

type degEvent struct {
	at    int64
	delta int
}

// violationIntervals 依据度事件流，计算在 [eraStart, eraEnd) 内度超出基数的区间。
// eraEnd 为 0 表示无上界。
func violationIntervals(evs []degEvent, card Card, eraStart, eraEnd int64) []diagIvl {
	// 同 tick 先聚合净变化，避免 +1/-1 抖动产生虚假区间。
	net := map[int64]int{}
	for _, e := range evs {
		net[e.at] += e.delta
	}
	var ticks []int64
	for t, d := range net {
		if d != 0 {
			ticks = append(ticks, t)
		}
	}
	sort.Slice(ticks, func(i, j int) bool { return ticks[i] < ticks[j] })

	var out []diagIvl
	violating := false
	var violLo int64
	violated := func(d int) bool {
		return d < card.Min || (card.Max != 0 && d > card.Max)
	}
	open := func(t int64) {
		if !violating {
			violating = true
			violLo = t
		}
	}
	closeAt := func(t int64) {
		if violating {
			violating = false
			if violLo < t {
				out = append(out, diagIvl{lo: violLo, hi: t})
			}
		}
	}
	degree := 0
	for _, t := range ticks {
		if t <= eraStart {
			degree += net[t]
		}
	}
	if violated(degree) {
		open(eraStart)
	}
	for _, t := range ticks {
		if t <= eraStart || (eraEnd != 0 && t >= eraEnd) {
			continue
		}
		degree += net[t]
		if violated(degree) {
			open(t)
		} else {
			closeAt(t)
		}
	}
	if eraEnd != 0 {
		closeAt(eraEnd)
	}
	return out
}

func buildEras(lt LinkType, rules []LinkTypeRule, diag [][]diagIvl, codes []halfCode) []*eraIndex {
	// 正向度：偶数半对应边 (a -> b)，记在 a 名下。
	// 反向度：同一条边记在 b 名下。
	fwdEvents := map[ID][]degEvent{}
	revEvents := map[ID][]degEvent{}
	for code := 0; code+1 < len(codes); code += 2 {
		joint := intersectIntervals(diag[code], diag[code+1])
		a := codes[code].a
		b := codes[code].b
		for _, in := range joint {
			fwdEvents[a] = append(fwdEvents[a],
				degEvent{at: in.lo, delta: 1},
				degEvent{at: in.hi, delta: -1})
			revEvents[b] = append(revEvents[b],
				degEvent{at: in.lo, delta: 1},
				degEvent{at: in.hi, delta: -1})
			// 对称链接：边天然双向，从 b 出发同样计入正向度，
			// 从 a 出发同样计入反向度。
			if lt.Symmetric {
				fwdEvents[b] = append(fwdEvents[b],
					degEvent{at: in.lo, delta: 1},
					degEvent{at: in.hi, delta: -1})
				revEvents[a] = append(revEvents[a],
					degEvent{at: in.lo, delta: 1},
					degEvent{at: in.hi, delta: -1})
			}
		}
	}
	fwdObjs := sortedKeys(fwdEvents)
	revObjs := sortedKeys(revEvents)

	eras := make([]*eraIndex, 0, len(rules))
	for ri, rule := range rules {
		end := int64(0)
		if ri+1 < len(rules) {
			end = rules[ri+1].FromRecord
		}
		e := &eraIndex{
			ruleStart: rule.FromRecord, ruleEnd: end,
			fwdObjs: fwdObjs, revObjs: revObjs,
			evFwd: aggregateDegTicks(fwdEvents),
			evRev: aggregateDegTicks(revEvents),
		}
		var fwdIvls, revIvls []ivl
		for oi, obj := range fwdObjs {
			for _, in := range violationIntervals(
				append([]degEvent(nil), fwdEvents[obj]...),
				rule.Card.Forward, rule.FromRecord, end) {
				fwdIvls = append(fwdIvls, ivl{lo: in.lo, hi: in.hi, val: oi})
			}
		}
		for oi, obj := range revObjs {
			for _, in := range violationIntervals(
				append([]degEvent(nil), revEvents[obj]...),
				rule.Card.Reverse, rule.FromRecord, end) {
				revIvls = append(revIvls, ivl{lo: in.lo, hi: in.hi, val: oi})
			}
		}
		e.fwd = newIntervalIndex(fwdIvls)
		e.rev = newIntervalIndex(revIvls)
		eras = append(eras, e)
	}
	return eras
}

func aggregateDegTicks(raw map[ID][]degEvent) map[ID][]degTick {
	out := make(map[ID][]degTick, len(raw))
	for obj, evs := range raw {
		net := map[int64]int{}
		for _, e := range evs {
			net[e.at] += e.delta
		}
		var ts []int64
		for t, d := range net {
			if d != 0 {
				ts = append(ts, t)
			}
		}
		sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
		ticks := make([]degTick, 0, len(ts))
		for _, t := range ts {
			ticks = append(ticks, degTick{at: t, delta: net[t]})
		}
		out[obj] = ticks
	}
	return out
}

func sortedKeys(m map[ID][]degEvent) []ID {
	var out []ID
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
