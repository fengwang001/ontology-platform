package escape

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// TestClassPriority 同时在 GLB 与 RR 内的分配点取全局逃逸。
func TestClassPriority(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "both", 0, 1, []Stmt{newStmt(0), retStmt(0), globalStmt(0)})
	checkSites(t, r, "both", []Site{{1, ClassGlobal}})
	checkSummary(t, r, "both", summ(0, true, nil, nil, nil), 1)

	// 返回逃逸优先于参数逃逸：对象既被返回又被存入参数。
	mustRegister(t, r, "retOverParam", 1, 2, []Stmt{newStmt(1), storeStmt(0, 1), retStmt(1)})
	checkSites(t, r, "retOverParam", []Site{{2, ClassReturn}})

	// 全局优先于参数：对象被存入参数又被标全局。
	mustRegister(t, r, "glbOverParam", 1, 2, []Stmt{newStmt(1), storeStmt(0, 1), globalStmt(1)})
	checkSites(t, r, "glbOverParam", []Site{{3, ClassGlobal}})
}

// TestClosureSemantics 验证 GLB/RR 沿 heap 边的闭包，以及 PR 至少走一步。
func TestClosureSemantics(t *testing.T) {
	r := NewRegistry()

	// GLB 闭包：标记 S1，heap(S1)={S2}，两者都全局逃逸。
	mustRegister(t, r, "glbCl", 0, 2, []Stmt{newStmt(0), newStmt(1), storeStmt(0, 1), globalStmt(0)})
	checkSites(t, r, "glbCl", []Site{{1, ClassGlobal}, {2, ClassGlobal}})

	// RR 闭包：ret={S3}，heap(S3)={S4}，两者都返回逃逸。
	mustRegister(t, r, "rrCl", 0, 2, []Stmt{newStmt(0), newStmt(1), storeStmt(0, 1), retStmt(0)})
	checkSites(t, r, "rrCl", []Site{{3, ClassReturn}, {4, ClassReturn}})

	// PR 至少一步：把参数对象存入分配点（heap(S5)={P0}），
	// 参数对象自身不在 PR 内，S5 也不在任何集合内，保持栈上。
	mustRegister(t, r, "prStep", 1, 2, []Stmt{newStmt(1), storeStmt(1, 0)})
	checkSites(t, r, "prStep", []Site{{5, ClassStack}})

	// 对照：把分配点存入参数（heap(P0)={S6}），S6 一步可达，参数逃逸。
	mustRegister(t, r, "prHit", 1, 2, []Stmt{newStmt(1), storeStmt(0, 1)})
	checkSites(t, r, "prHit", []Site{{6, ClassParam}})

	// PR 多步闭包：heap(P0)={S7}，heap(S7)={S8}，两者都参数逃逸。
	mustRegister(t, r, "prCl", 1, 3, []Stmt{newStmt(1), newStmt(2), storeStmt(0, 1), storeStmt(1, 2)})
	checkSites(t, r, "prCl", []Site{{7, ClassParam}, {8, ClassParam}})
}

// TestLoadSemantics 对参数对象 Load：无 Store 时为空；先 Store 后能取到。
func TestLoadSemantics(t *testing.T) {
	r := NewRegistry()

	// 无 Store：Load(1,0) 得到空集，ret 为空，摘要全假。
	mustRegister(t, r, "loadEmpty", 1, 2, []Stmt{loadStmt(1, 0), retStmt(1)})
	checkSummary(t, r, "loadEmpty", summ(1, false, []bool{false}, []bool{false}, nil), 1)

	// 先 Store 后 Load：heap(P0)={S1}，Load 取回 S1 并返回。
	mustRegister(t, r, "loadStored", 1, 3, []Stmt{newStmt(2), storeStmt(0, 2), loadStmt(1, 0), retStmt(1)})
	checkSummary(t, r, "loadStored", summ(1, true, []bool{false}, []bool{false}, nil), 1)
	checkSites(t, r, "loadStored", []Site{{1, ClassReturn}})
}

// TestCallResultObject 验证 ret[i]、fresh 与调用结果对象 X 对调用者的影响。
func TestCallResultObject(t *testing.T) {
	r := NewRegistry()

	// mk：fresh 真。调用者接收结果并返回：X 直接进入 ret，调用者 fresh 真。
	mustRegister(t, r, "mk", 0, 1, []Stmt{newStmt(0), retStmt(0)})
	mustRegister(t, r, "useX", 0, 1, []Stmt{callStmt(0, "mk"), retStmt(0)})
	checkSummary(t, r, "useX", summ(0, true, nil, nil, nil), 1)

	// X 一开始带全局标记：把本函数的分配点存入 X（heap(X)={S2}），
	// GLB 闭包从 X 到达 S2，S2 全局逃逸。
	mustRegister(t, r, "xSeed", 0, 2, []Stmt{newStmt(0), callStmt(1, "mk"), storeStmt(1, 0)})
	checkSites(t, r, "xSeed", []Site{{2, ClassGlobal}})

	// ret[i] 为真使调用结果获得实参指向：实参对象成为返回逃逸，
	// 未被触及的分配点保持栈上。
	mustRegister(t, r, "id", 1, 1, []Stmt{retStmt(0)})
	checkSummary(t, r, "id", summ(1, false, []bool{true}, []bool{false}, nil), 1)
	mustRegister(t, r, "caller", 0, 3, []Stmt{newStmt(0), newStmt(1), callStmt(2, "id", 0), retStmt(2)})
	checkSummary(t, r, "caller", summ(0, true, nil, nil, nil), 1)
	checkSites(t, r, "caller", []Site{{3, ClassReturn}, {4, ClassStack}})
}

// TestDiscardedResult 结果被丢弃时不产生任何指向与标记（X 也不存在）。
func TestDiscardedResult(t *testing.T) {
	r := NewRegistry()
	// id：ret[0] 真、fresh 假。gg：ret[0] 真、fresh 真（同规格示例 g）。
	mustRegister(t, r, "id", 1, 1, []Stmt{retStmt(0)})
	mustRegister(t, r, "gg", 1, 2, []Stmt{newStmt(1), storeStmt(1, 0), retStmt(1)})

	// 丢弃 id 的结果：ret[0] 不生效，v1 无指向，Global(1) 无标记。
	mustRegister(t, r, "dropRet", 0, 2, []Stmt{newStmt(0), callStmt(-1, "id", 0), globalStmt(1)})
	checkSites(t, r, "dropRet", []Site{{2, ClassStack}})

	// 丢弃 gg 的结果：ret[0] 不生效且不产生 X，v1 无指向，分配点栈上。
	mustRegister(t, r, "dropFresh", 0, 2, []Stmt{newStmt(0), callStmt(-1, "gg", 0), globalStmt(1)})
	checkSummary(t, r, "dropFresh", summ(0, false, nil, nil, nil), 1)
	checkSites(t, r, "dropFresh", []Site{{3, ClassStack}})

	// 对照：不丢弃时 pts(v1) 含 S4 与 X，Global(1) 标记二者，S4 全局逃逸。
	mustRegister(t, r, "keepFresh", 0, 2, []Stmt{newStmt(0), callStmt(1, "gg", 0), globalStmt(1)})
	checkSites(t, r, "keepFresh", []Site{{4, ClassGlobal}})
}

// TestEAndGlobAloneInsufficient E 的传播与 glob 的传播各自单独不足以
// 得到正确分类：只有 E 时无标记，只有 glob 时够不到存入的对象。
func TestEAndGlobAloneInsufficient(t *testing.T) {
	r := NewRegistry()
	// f2：E[0][1] 真、glob 全假。gb：glob[0] 真、E 全假。
	mustRegister(t, r, "f2", 2, 2, []Stmt{storeStmt(0, 1)})
	mustRegister(t, r, "gb", 1, 1, []Stmt{globalStmt(0)})

	// 只有 E：heap(S1)={S2} 但无全局标记，两者都栈上。
	mustRegister(t, r, "onlyE", 0, 2, []Stmt{newStmt(0), newStmt(1), callStmt(-1, "f2", 0, 1)})
	checkSites(t, r, "onlyE", []Site{{1, ClassStack}, {2, ClassStack}})

	// 只有 glob：S3 被标记，但 S4 没有经 E 挂到 S3 上，S4 栈上。
	mustRegister(t, r, "onlyGlob", 0, 2, []Stmt{newStmt(0), newStmt(1), callStmt(-1, "gb", 0)})
	checkSites(t, r, "onlyGlob", []Site{{3, ClassGlobal}, {4, ClassStack}})

	// 两者齐备：E 把 S6 挂到 S5 上，glob 标记 S5，两者都全局逃逸。
	mustRegister(t, r, "both", 0, 2, []Stmt{newStmt(0), newStmt(1), callStmt(-1, "f2", 0, 1), globalStmt(0)})
	checkSites(t, r, "both", []Site{{5, ClassGlobal}, {6, ClassGlobal}})
}

// TestSelfRecursionFixpoint 自递归不动点：轮数、单调性与上界。
func TestSelfRecursionFixpoint(t *testing.T) {
	r := NewRegistry()

	// glob 沿自调用逐轮传播：3 轮，glob=[真,真]。
	mustRegister(t, r, "r", 2, 2, []Stmt{callStmt(-1, "r", 1, 0), globalStmt(0)})
	checkSummary(t, r, "r", summ(2, false, nil, []bool{true, true}, nil), 3)

	// fresh 经自调用变得为真：第 1 轮 fresh 假，第 2 轮起为真，共 2 轮。
	mustRegister(t, r, "recF", 0, 1, []Stmt{newStmt(0), callStmt(0, "recF"), retStmt(0)})
	checkSummary(t, r, "recF", summ(0, true, nil, nil, nil), 2)
	checkSites(t, r, "recF", []Site{{1, ClassReturn}})

	// E 沿自调用传播：E[0][1] 真后，换序自调用使 E[1][0] 也为真，共 3 轮。
	mustRegister(t, r, "recE", 2, 2, []Stmt{storeStmt(0, 1), callStmt(-1, "recE", 1, 0)})
	checkSummary(t, r, "recE", summ(2, false, nil, nil, [][2]int{{0, 1}, {1, 0}}), 3)

	// 轮数上界 2k+k²+2。
	for _, name := range []string{"r", "recF", "recE"} {
		f := r.funcs[name]
		if bound := 2*f.k + f.k*f.k + 2; f.rounds > bound {
			t.Errorf("%s: rounds %d exceeds bound %d", name, f.rounds, bound)
		}
	}
}

// TestAnalysisRunsCounter 非导出计数器：实际分析轮数等于各函数轮数之和。
func TestAnalysisRunsCounter(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "a", 0, 1, []Stmt{newStmt(0), retStmt(0)})
	mustRegister(t, r, "b", 2, 2, []Stmt{callStmt(-1, "b", 1, 0), globalStmt(0)})
	mustRegister(t, r, "c", 1, 1, []Stmt{retStmt(0)})
	total := 0
	for _, name := range []string{"a", "b", "c"} {
		_, rounds, err := r.Summary(name)
		if err != nil {
			t.Fatal(err)
		}
		total += rounds
	}
	if r.analysisRuns != total {
		t.Errorf("analysisRuns = %d, sum of rounds = %d", r.analysisRuns, total)
	}
}

// TestShuffleInvariance 打乱同一函数内语句次序，摘要与分类多重集不变。
func TestShuffleInvariance(t *testing.T) {
	base := []Stmt{
		newStmt(2), newStmt(3),
		storeStmt(0, 2), storeStmt(2, 3),
		loadStmt(1, 0), retStmt(1),
	}
	r := NewRegistry()
	mustRegister(t, r, "base", 1, 4, base)
	wantSumm, _, _ := r.Summary("base")
	wantSites, _ := r.Sites("base")
	wantClasses := make([]int, len(wantSites))
	for i, s := range wantSites {
		wantClasses[i] = int(s.Class)
	}
	sort.Ints(wantClasses)

	rng := rand.New(rand.NewSource(7))
	for n := 0; n < 30; n++ {
		perm := rng.Perm(len(base))
		shuffled := make([]Stmt, len(base))
		for i, j := range perm {
			shuffled[i] = base[j]
		}
		name := "sh" + string(rune('a'+n))
		mustRegister(t, r, name, 1, 4, shuffled)
		gotSumm, rounds, _ := r.Summary(name)
		if !reflect.DeepEqual(gotSumm, wantSumm) {
			t.Fatalf("perm %v: summary %+v, want %+v", perm, gotSumm, wantSumm)
		}
		if rounds != 1 {
			t.Fatalf("perm %v: rounds %d, want 1", perm, rounds)
		}
		gotSites, _ := r.Sites(name)
		gotClasses := make([]int, len(gotSites))
		for i, s := range gotSites {
			gotClasses[i] = int(s.Class)
		}
		sort.Ints(gotClasses)
		if !reflect.DeepEqual(gotClasses, wantClasses) {
			t.Fatalf("perm %v: classes %v, want %v", perm, gotClasses, wantClasses)
		}
	}
}
