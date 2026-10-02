package escape

import (
	"fmt"
	"math/rand"
	"testing"
)

// --- 朴素模拟：按规则逐步写成，反复扫描全部语句直到无变化 ---

type naiveSet map[int]bool

func naiveUnion(dst, src naiveSet) bool {
	changed := false
	for x := range src {
		if !dst[x] {
			dst[x] = true
			changed = true
		}
	}
	return changed
}

type naiveOutcome struct {
	summ    Summary
	classes []Class
	rounds  int
}

// naiveAnalyze 对单个函数做一轮朴素分析。self 为自调用本轮所用摘要，
// callees 为已登记函数的摘要。
func naiveAnalyze(fn genFunc, self Summary, callees map[string]Summary) (Summary, []Class) {
	k := fn.k
	ns := 0
	for _, st := range fn.stmts {
		if st.Kind == New {
			ns++
		}
	}
	// 对象编号：分配点 0..ns-1，参数 ns..ns+k-1，X 在其后。
	xOf := map[int]int{} // 语句下标 -> X 对象编号
	nObj := ns + k
	siteOf := map[int]int{}
	siteIdx := 0
	for i, st := range fn.stmts {
		if st.Kind == New {
			siteOf[i] = siteIdx
			siteIdx++
		}
		if st.Kind == Call && st.D != -1 {
			cs := callees[st.G]
			if st.G == fn.name {
				cs = self
			}
			if cs.Fresh {
				xOf[i] = nObj
				nObj++
			}
		}
	}

	pts := make([]naiveSet, fn.v)
	for i := range pts {
		pts[i] = naiveSet{}
	}
	for i := 0; i < k; i++ {
		pts[i][ns+i] = true
	}
	heap := make([]naiveSet, nObj)
	for i := range heap {
		heap[i] = naiveSet{}
	}
	ret := naiveSet{}
	gmark := naiveSet{}
	for _, x := range xOf {
		gmark[x] = true
	}

	// 反复扫描全部语句直到无变化。
	for changed := true; changed; {
		changed = false
		for i, st := range fn.stmts {
			switch st.Kind {
			case New:
				if !pts[st.D][siteOf[i]] {
					pts[st.D][siteOf[i]] = true
					changed = true
				}
			case Copy:
				changed = naiveUnion(pts[st.D], pts[st.S]) || changed
			case Store:
				for o := range pts[st.D] {
					changed = naiveUnion(heap[o], pts[st.S]) || changed
				}
			case Load:
				for o := range pts[st.S] {
					changed = naiveUnion(pts[st.D], heap[o]) || changed
				}
			case Ret:
				changed = naiveUnion(ret, pts[st.S]) || changed
			case Global:
				for o := range pts[st.S] {
					if !gmark[o] {
						gmark[o] = true
						changed = true
					}
				}
			case Call:
				cs := callees[st.G]
				if st.G == fn.name {
					cs = self
				}
				for a := range st.Args {
					if cs.Glob[a] {
						for o := range pts[st.Args[a]] {
							if !gmark[o] {
								gmark[o] = true
								changed = true
							}
						}
					}
					if cs.Ret[a] && st.D != -1 {
						changed = naiveUnion(pts[st.D], pts[st.Args[a]]) || changed
					}
					for b := range st.Args {
						if cs.E[a][b] {
							for o := range pts[st.Args[a]] {
								changed = naiveUnion(heap[o], pts[st.Args[b]]) || changed
							}
						}
					}
				}
				if cs.Fresh && st.D != -1 {
					if !pts[st.D][xOf[i]] {
						pts[st.D][xOf[i]] = true
						changed = true
					}
				}
			}
		}
	}

	// 闭包：反复松弛直到无变化。
	reach := func(seed naiveSet) naiveSet {
		out := naiveSet{}
		naiveUnion(out, seed)
		for changed := true; changed; {
			changed = false
			for o := range out {
				changed = naiveUnion(out, heap[o]) || changed
			}
		}
		return out
	}
	glb := reach(gmark)
	rr := reach(ret)
	prSeed := naiveSet{}
	for i := 0; i < k; i++ {
		naiveUnion(prSeed, heap[ns+i])
	}
	pr := reach(prSeed)

	classes := make([]Class, ns)
	for s := 0; s < ns; s++ {
		switch {
		case glb[s]:
			classes[s] = ClassGlobal
		case rr[s]:
			classes[s] = ClassReturn
		case pr[s]:
			classes[s] = ClassParam
		default:
			classes[s] = ClassStack
		}
	}

	summ := zeroSummary(k)
	for i := 0; i < k; i++ {
		summ.Ret[i] = rr[ns+i]
		summ.Glob[i] = glb[ns+i]
		for j := 0; j < k; j++ {
			summ.E[i][j] = heap[ns+i][ns+j]
		}
	}
	xObjs := naiveSet{}
	for _, x := range xOf {
		xObjs[x] = true
	}
	for o := range ret {
		if o < ns || xObjs[o] {
			summ.Fresh = true
			break
		}
	}
	return summ, classes
}

// naiveRun 朴素地登记整个序列，返回每个函数的结果。
// 自递归迭代时顺带验证摘要每位只会由假变真。
func naiveRun(t *testing.T, seq []genFunc) map[string]naiveOutcome {
	t.Helper()
	callees := map[string]Summary{}
	out := map[string]naiveOutcome{}
	for _, fn := range seq {
		selfCall := false
		for _, st := range fn.stmts {
			if st.Kind == Call && st.G == fn.name {
				selfCall = true
			}
		}
		var summ Summary
		var classes []Class
		rounds := 0
		if !selfCall {
			summ, classes = naiveAnalyze(fn, zeroSummary(fn.k), callees)
			rounds = 1
		} else {
			cur := zeroSummary(fn.k)
			for {
				next, cl := naiveAnalyze(fn, cur, callees)
				rounds++
				for i := 0; i < fn.k; i++ {
					if cur.Ret[i] && !next.Ret[i] || cur.Glob[i] && !next.Glob[i] {
						t.Fatalf("%s: summary bit flipped back to false", fn.name)
					}
					for j := 0; j < fn.k; j++ {
						if cur.E[i][j] && !next.E[i][j] {
							t.Fatalf("%s: E[%d][%d] flipped back to false", fn.name, i, j)
						}
					}
				}
				if cur.Fresh && !next.Fresh {
					t.Fatalf("%s: fresh flipped back to false", fn.name)
				}
				if summaryEqual(next, cur) {
					summ, classes = next, cl
					break
				}
				cur = next
			}
		}
		callees[fn.name] = summ
		out[fn.name] = naiveOutcome{summ: summ, classes: classes, rounds: rounds}
	}
	return out
}

// --- 随机函数序列生成 ---

type genFunc struct {
	name  string
	k, v  int
	stmts []Stmt
}

func genSequence(rng *rand.Rand, id int) []genFunc {
	n := 1 + rng.Intn(5)
	seq := make([]genFunc, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("s%df%d", id, i)
		k := rng.Intn(4)
		v := k + rng.Intn(7-k)
		if v == 0 {
			v = 1
		}
		nstmts := 1 + rng.Intn(12)
		fn := genFunc{name: name, k: k, v: v}
		for j := 0; j < nstmts; j++ {
			rv := func() int { return rng.Intn(v) }
			switch rng.Intn(8) {
			case 0:
				fn.stmts = append(fn.stmts, newStmt(rv()))
			case 1:
				fn.stmts = append(fn.stmts, copyStmt(rv(), rv()))
			case 2:
				fn.stmts = append(fn.stmts, storeStmt(rv(), rv()))
			case 3:
				fn.stmts = append(fn.stmts, loadStmt(rv(), rv()))
			case 4:
				fn.stmts = append(fn.stmts, retStmt(rv()))
			case 5:
				fn.stmts = append(fn.stmts, globalStmt(rv()))
			default: // Call
				callee := name
				ck := k
				if len(seq) > 0 && rng.Intn(3) != 0 {
					prev := seq[rng.Intn(len(seq))]
					callee, ck = prev.name, prev.k
				}
				args := make([]int, ck)
				for a := range args {
					args[a] = rv()
				}
				d := -1
				if rng.Intn(3) != 0 {
					d = rv()
				}
				fn.stmts = append(fn.stmts, callStmt(d, callee, args...))
			}
		}
		seq = append(seq, fn)
	}
	return seq
}

// TestRandomAgainstNaive 2000 组随机函数序列与朴素模拟对照。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const trials = 2000
	for iter := 0; iter < trials; iter++ {
		seq := genSequence(rng, iter)
		want := naiveRun(t, seq)
		r := NewRegistry()
		for _, fn := range seq {
			if err := r.Register(fn.name, fn.k, fn.v, fn.stmts); err != nil {
				t.Fatalf("iter %d: Register(%q) failed: %v", iter, fn.name, err)
			}
		}
		nextSite := 1
		totalRounds := 0
		for _, fn := range seq {
			w := want[fn.name]
			gotSumm, gotRounds, err := r.Summary(fn.name)
			if err != nil {
				t.Fatalf("iter %d: %v", iter, err)
			}
			gotSites, _ := r.Sites(fn.name)
			ok := gotRounds == w.rounds && summaryEqual(gotSumm, w.summ) && len(gotSites) == len(w.classes)
			siteIDs := make([]int, len(gotSites))
			for i, s := range gotSites {
				siteIDs[i] = s.ID
				ok = ok && s.Class == w.classes[i] && s.ID == nextSite
				nextSite++
			}
			// 自递归轮数上界。
			if gotRounds > 2*fn.k+fn.k*fn.k+2 {
				ok = false
			}
			totalRounds += gotRounds
			t.Logf("iter %d input %s(k=%d,V=%d)%v", iter, fn.name, fn.k, fn.v, fn.stmts)
			t.Logf("  output summary=%+v rounds=%d sites=%v classes=%v", gotSumm, gotRounds, siteIDs, gotSites)
			t.Logf("  naive  summary=%+v rounds=%d classes=%v", w.summ, w.rounds, w.classes)
			if !ok {
				t.Fatalf("iter %d %s: mismatch (see log); got summary=%+v rounds=%d sites=%v, want summary=%+v rounds=%d classes=%v",
					iter, fn.name, gotSumm, gotRounds, gotSites, w.summ, w.rounds, w.classes)
			}
		}
		if r.analysisRuns != totalRounds {
			t.Fatalf("iter %d: analysisRuns=%d, sum of rounds=%d", iter, r.analysisRuns, totalRounds)
		}
	}
}
