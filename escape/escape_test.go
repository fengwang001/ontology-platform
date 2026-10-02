package escape

import (
	"errors"
	"reflect"
	"testing"
)

// --- 构造语句的辅助函数 ---

func newStmt(d int) Stmt      { return Stmt{Kind: New, D: d} }
func copyStmt(d, s int) Stmt  { return Stmt{Kind: Copy, D: d, S: s} }
func storeStmt(d, s int) Stmt { return Stmt{Kind: Store, D: d, S: s} }
func loadStmt(d, s int) Stmt  { return Stmt{Kind: Load, D: d, S: s} }
func retStmt(s int) Stmt      { return Stmt{Kind: Ret, S: s} }
func globalStmt(s int) Stmt   { return Stmt{Kind: Global, S: s} }
func callStmt(d int, g string, args ...int) Stmt {
	return Stmt{Kind: Call, D: d, G: g, Args: args}
}

func mustRegister(t *testing.T, r *Registry, name string, k, v int, stmts []Stmt) {
	t.Helper()
	if err := r.Register(name, k, v, stmts); err != nil {
		t.Fatalf("Register(%q) failed: %v", name, err)
	}
}

func errKind(err error) ErrorKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return -1
}

func checkSummary(t *testing.T, r *Registry, name string, want Summary, wantRounds int) {
	t.Helper()
	got, rounds, err := r.Summary(name)
	if err != nil {
		t.Fatalf("Summary(%q): %v", name, err)
	}
	if rounds != wantRounds {
		t.Errorf("Summary(%q) rounds = %d, want %d", name, rounds, wantRounds)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Summary(%q) = %+v, want %+v", name, got, want)
	}
}

func checkSites(t *testing.T, r *Registry, name string, want []Site) {
	t.Helper()
	got, err := r.Sites(name)
	if err != nil {
		t.Fatalf("Sites(%q): %v", name, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sites(%q) = %+v, want %+v", name, got, want)
	}
}

func summ(k int, fresh bool, ret, glob []bool, e [][2]int) Summary {
	s := zeroSummary(k)
	s.Fresh = fresh
	copy(s.Ret, ret)
	copy(s.Glob, glob)
	for _, p := range e {
		s.E[p[0]][p[1]] = true
	}
	return s
}

// TestSpecExamples 复现任务说明中的全部示例。
func TestSpecExamples(t *testing.T) {
	r := NewRegistry()

	// mk(k=0,V=1)[New(0), Ret(0)]：分配点 1 返回逃逸，fresh 真。
	mustRegister(t, r, "mk", 0, 1, []Stmt{newStmt(0), retStmt(0)})
	checkSummary(t, r, "mk", summ(0, true, nil, nil, nil), 1)
	checkSites(t, r, "mk", []Site{{1, ClassReturn}})

	// g(k=1,V=2)[New(1), Store(1,0), Ret(1)]：分配点 2 返回逃逸，
	// ret[0] 真、glob[0] 假、E 全假、fresh 真。
	mustRegister(t, r, "g", 1, 2, []Stmt{newStmt(1), storeStmt(1, 0), retStmt(1)})
	checkSummary(t, r, "g", summ(1, true, []bool{true}, []bool{false}, nil), 1)
	checkSites(t, r, "g", []Site{{2, ClassReturn}})

	// h(k=0,V=2)[New(0), Call(1,g,[0]), Global(1)]：分配点 3 全局逃逸。
	mustRegister(t, r, "h", 0, 2, []Stmt{newStmt(0), callStmt(1, "g", 0), globalStmt(1)})
	checkSummary(t, r, "h", summ(0, false, nil, nil, nil), 1)
	checkSites(t, r, "h", []Site{{3, ClassGlobal}})

	// h2(k=0,V=1)[New(0), Call(-1,g,[0])]：分配点 4 栈上（结果被丢弃）。
	mustRegister(t, r, "h2", 0, 1, []Stmt{newStmt(0), callStmt(-1, "g", 0)})
	checkSummary(t, r, "h2", summ(0, false, nil, nil, nil), 1)
	checkSites(t, r, "h2", []Site{{4, ClassStack}})

	// r(k=2,V=2)[Call(-1,r,[1,0]), Global(0)]：3 轮，glob=[真,真]。
	mustRegister(t, r, "r", 2, 2, []Stmt{callStmt(-1, "r", 1, 0), globalStmt(0)})
	checkSummary(t, r, "r", summ(2, false, nil, []bool{true, true}, nil), 3)

	// f2(k=2,V=2)[Store(0,1)]：E[0][1] 真。
	mustRegister(t, r, "f2", 2, 2, []Stmt{storeStmt(0, 1)})
	checkSummary(t, r, "f2", summ(2, false, nil, nil, [][2]int{{0, 1}}), 1)

	// c(k=0,V=2)[New(0), New(1), Call(-1,f2,[0,1]), Global(0)]：
	// 分配点 5、6 都全局逃逸（E 传播）。
	mustRegister(t, r, "c", 0, 2, []Stmt{newStmt(0), newStmt(1), callStmt(-1, "f2", 0, 1), globalStmt(0)})
	checkSummary(t, r, "c", summ(0, false, nil, nil, nil), 1)
	checkSites(t, r, "c", []Site{{5, ClassGlobal}, {6, ClassGlobal}})

	// p(k=1,V=2)[New(1), Store(0,1)]：分配点 7 参数逃逸。
	mustRegister(t, r, "p", 1, 2, []Stmt{newStmt(1), storeStmt(0, 1)})
	checkSummary(t, r, "p", summ(1, false, []bool{false}, []bool{false}, nil), 1)
	checkSites(t, r, "p", []Site{{7, ClassParam}})
}

// TestNotFound 查询未登记的名字报不存在。
func TestNotFound(t *testing.T) {
	r := NewRegistry()
	if _, _, err := r.Summary("nope"); errKind(err) != ErrNotFound {
		t.Errorf("Summary on missing name: kind = %v, want ErrNotFound", errKind(err))
	}
	if _, err := r.Sites("nope"); errKind(err) != ErrNotFound {
		t.Errorf("Sites on missing name: kind = %v, want ErrNotFound", errKind(err))
	}
}
