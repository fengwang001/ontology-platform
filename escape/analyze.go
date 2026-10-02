package escape

// zeroSummary 返回 k 个参数的全假摘要。
func zeroSummary(k int) Summary {
	e := make([][]bool, k)
	for i := range e {
		e[i] = make([]bool, k)
	}
	return Summary{Ret: make([]bool, k), Glob: make([]bool, k), E: e}
}

func summaryEqual(a, b Summary) bool {
	if a.Fresh != b.Fresh || len(a.Ret) != len(b.Ret) {
		return false
	}
	for i := range a.Ret {
		if a.Ret[i] != b.Ret[i] || a.Glob[i] != b.Glob[i] {
			return false
		}
		for j := range a.Ret {
			if a.E[i][j] != b.E[i][j] {
				return false
			}
		}
	}
	return true
}

// analyzeToFixpoint 对函数做完整分析：无自调用时恰 1 轮；
// 有自调用时从全假摘要出发迭代，直到摘要不变，轮数计入 f.rounds。
func (r *Registry) analyzeToFixpoint(f *function) {
	selfCall := false
	for _, st := range f.stmts {
		if st.Kind == Call && st.G == f.name {
			selfCall = true
			break
		}
	}
	if !selfCall {
		f.summary, f.classes = r.analyzeOnce(f, zeroSummary(f.k))
		f.rounds = 1
		return
	}
	cur := zeroSummary(f.k)
	for {
		next, classes := r.analyzeOnce(f, cur)
		f.rounds++
		if summaryEqual(next, cur) {
			f.summary = next
			f.classes = classes
			return
		}
		cur = next
	}
}

// objSet 是对象编号的集合。
type objSet map[int]struct{}

func addTo(s objSet, x int) bool {
	if _, ok := s[x]; ok {
		return false
	}
	s[x] = struct{}{}
	return true
}

// unionInto 把 src 并入 dst，返回是否有变化。dst 与 src 为同一集合时安全。
func unionInto(dst, src objSet) bool {
	changed := false
	for x := range src {
		if addTo(dst, x) {
			changed = true
		}
	}
	return changed
}

// analyzeOnce 执行一轮流不敏感指向分析（最小不动点），
// 返回本轮摘要与各分配点分类。self 为自调用本轮所用的摘要。
func (r *Registry) analyzeOnce(f *function, self Summary) (Summary, []Class) {
	r.analysisRuns++

	k, v := f.k, f.v
	ns := len(f.siteIDs)

	// 对象编号：0..ns-1 为各分配点对象，ns..ns+k-1 为参数对象 P0..P(k-1)，
	// 其后为各 Call 语句（d != -1 且被调摘要 fresh 为真）的调用结果对象 X。
	callee := make([]*Summary, len(f.stmts))
	xObj := make([]int, len(f.stmts))
	nObj := ns + k
	for i, st := range f.stmts {
		xObj[i] = -1
		if st.Kind != Call {
			continue
		}
		if st.G == f.name {
			callee[i] = &self
		} else {
			callee[i] = &r.funcs[st.G].summary
		}
		if st.D != -1 && callee[i].Fresh {
			xObj[i] = nObj
			nObj++
		}
	}

	// siteObj[i] 为第 i 条语句的分配点对象编号（非 New 为 -1）。
	siteObj := make([]int, len(f.stmts))
	siteCount := 0
	for i, st := range f.stmts {
		siteObj[i] = -1
		if st.Kind == New {
			siteObj[i] = siteCount
			siteCount++
		}
	}

	pts := make([]objSet, v)
	for i := range pts {
		pts[i] = objSet{}
	}
	for i := 0; i < k; i++ {
		pts[i][ns+i] = struct{}{}
	}
	heap := make([]objSet, nObj)
	for i := range heap {
		heap[i] = objSet{}
	}
	ret := objSet{}
	gmark := objSet{}
	// 调用结果对象 X 一开始就带全局标记。
	for _, x := range xObj {
		if x >= 0 {
			gmark[x] = struct{}{}
		}
	}

	// 反复扫描全部语句直到无变化（最小不动点）。
	for changed := true; changed; {
		changed = false
		for i, st := range f.stmts {
			switch st.Kind {
			case New:
				if addTo(pts[st.D], siteObj[i]) {
					changed = true
				}
			case Copy:
				if unionInto(pts[st.D], pts[st.S]) {
					changed = true
				}
			case Store:
				for o := range pts[st.D] {
					if unionInto(heap[o], pts[st.S]) {
						changed = true
					}
				}
			case Load:
				for o := range pts[st.S] {
					if unionInto(pts[st.D], heap[o]) {
						changed = true
					}
				}
			case Ret:
				if unionInto(ret, pts[st.S]) {
					changed = true
				}
			case Global:
				for o := range pts[st.S] {
					if addTo(gmark, o) {
						changed = true
					}
				}
			case Call:
				cs := callee[i]
				for a := range st.Args {
					if cs.Glob[a] {
						for o := range pts[st.Args[a]] {
							if addTo(gmark, o) {
								changed = true
							}
						}
					}
					if cs.Ret[a] && st.D != -1 {
						if unionInto(pts[st.D], pts[st.Args[a]]) {
							changed = true
						}
					}
					for b := range st.Args {
						if !cs.E[a][b] {
							continue
						}
						for o := range pts[st.Args[a]] {
							if unionInto(heap[o], pts[st.Args[b]]) {
								changed = true
							}
						}
					}
				}
				if cs.Fresh && st.D != -1 {
					if addTo(pts[st.D], xObj[i]) {
						changed = true
					}
				}
			}
		}
	}

	// 三种标记：GLB、RR 为沿 heap 边可达的闭包（含种子），
	// PR 为参数对象沿 heap 边至少走一步可达的对象集合。
	glb := closure(gmark, heap)
	rr := closure(ret, heap)
	prSeed := objSet{}
	for i := 0; i < k; i++ {
		unionInto(prSeed, heap[ns+i])
	}
	pr := closure(prSeed, heap)

	classes := make([]Class, ns)
	for s := 0; s < ns; s++ {
		switch {
		case has(glb, s):
			classes[s] = ClassGlobal
		case has(rr, s):
			classes[s] = ClassReturn
		case has(pr, s):
			classes[s] = ClassParam
		default:
			classes[s] = ClassStack
		}
	}

	summ := zeroSummary(k)
	for i := 0; i < k; i++ {
		summ.Ret[i] = has(rr, ns+i)
		summ.Glob[i] = has(glb, ns+i)
		for j := 0; j < k; j++ {
			summ.E[i][j] = has(heap[ns+i], ns+j)
		}
	}
	// fresh：ret 直接含某个分配点对象或某个 X 对象。
	for o := range ret {
		if o < ns || xObjOf(o, xObj) {
			summ.Fresh = true
			break
		}
	}
	return summ, classes
}

func has(s objSet, x int) bool {
	_, ok := s[x]
	return ok
}

// xObjOf 报告对象 o 是否为某个 Call 语句的 X 对象。
func xObjOf(o int, xObj []int) bool {
	for _, x := range xObj {
		if x == o {
			return true
		}
	}
	return false
}

// closure 返回种子集合沿 heap 边可达的闭包（含种子自身）。
func closure(seed objSet, heap []objSet) objSet {
	out := objSet{}
	stack := make([]int, 0, len(seed))
	for o := range seed {
		if addTo(out, o) {
			stack = append(stack, o)
		}
	}
	for len(stack) > 0 {
		o := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for x := range heap[o] {
			if addTo(out, x) {
				stack = append(stack, x)
			}
		}
	}
	return out
}
