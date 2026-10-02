package escape

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func expectErr(t *testing.T, r *Registry, want ErrorKind, name string, k, v int, stmts []Stmt) {
	t.Helper()
	err := r.Register(name, k, v, stmts)
	if err == nil {
		t.Fatalf("Register(%q) succeeded, want error kind %d", name, want)
	}
	if got := errKind(err); got != want {
		t.Fatalf("Register(%q) error kind = %d (%v), want %d", name, got, err, want)
	}
}

// TestInvalidArgs 参数非法的各种情形，以及恰好达到上限时允许。
func TestInvalidArgs(t *testing.T) {
	r := NewRegistry()

	// 名字：1 到 32 字节。
	expectErr(t, r, ErrInvalidArgs, "", 0, 0, nil)
	expectErr(t, r, ErrInvalidArgs, strings.Repeat("n", 33), 0, 0, nil)
	mustRegister(t, r, strings.Repeat("n", 32), 0, 0, nil)

	// k：0 到 8。
	expectErr(t, r, ErrInvalidArgs, "kneg", -1, 0, nil)
	expectErr(t, r, ErrInvalidArgs, "k9", 9, 9, nil)
	mustRegister(t, r, "k8", 8, 8, nil)

	// V：k 到 32。
	expectErr(t, r, ErrInvalidArgs, "vlt", 2, 1, nil)
	expectErr(t, r, ErrInvalidArgs, "v33", 0, 33, nil)
	mustRegister(t, r, "v32", 0, 32, nil)

	// 语句数：至多 64。
	stmts64 := make([]Stmt, 64)
	for i := range stmts64 {
		stmts64[i] = copyStmt(0, 0)
	}
	mustRegister(t, r, "s64", 0, 1, stmts64)
	expectErr(t, r, ErrInvalidArgs, "s65", 0, 1, append(append([]Stmt(nil), stmts64...), copyStmt(0, 0)))

	// 变量编号越界。
	expectErr(t, r, ErrInvalidArgs, "var1", 1, 2, []Stmt{newStmt(2)})
	expectErr(t, r, ErrInvalidArgs, "var2", 1, 2, []Stmt{copyStmt(0, -2)})
	expectErr(t, r, ErrInvalidArgs, "var3", 1, 2, []Stmt{retStmt(9)})
	expectErr(t, r, ErrInvalidArgs, "var4", 1, 2, []Stmt{callStmt(0, "var4", 2)})

	// d 为 -1 用于非 Call。
	expectErr(t, r, ErrInvalidArgs, "dneg", 0, 1, []Stmt{newStmt(-1)})
	expectErr(t, r, ErrInvalidArgs, "dneg2", 0, 1, []Stmt{loadStmt(-1, 0)})

	// 种类非法。
	expectErr(t, r, ErrInvalidArgs, "kind", 0, 1, []Stmt{{Kind: Kind(7), D: 0}})
	expectErr(t, r, ErrInvalidArgs, "kindneg", 0, 1, []Stmt{{Kind: Kind(-1), D: 0}})

	// args 个数越界：至多 8（与被调参数个数无关，属于参数非法）。
	mustRegister(t, r, "wide", 8, 8, nil)
	mustRegister(t, r, "args8", 0, 8, []Stmt{callStmt(-1, "wide", 0, 1, 2, 3, 4, 5, 6, 7)})
	expectErr(t, r, ErrInvalidArgs, "args9", 0, 9, []Stmt{callStmt(-1, "wide", 0, 1, 2, 3, 4, 5, 6, 7, 8)})
}

// TestRejectOrdering 拒绝原因按次序只报第一个。
func TestRejectOrdering(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "dup", 1, 1, []Stmt{retStmt(0)})

	// 参数非法优先于名字已登记。
	expectErr(t, r, ErrInvalidArgs, "dup", 99, 99, nil)
	// 名字已登记优先于被调检查。
	expectErr(t, r, ErrDuplicateName, "dup", 0, 1, []Stmt{callStmt(-1, "ghost")})

	// 按语句先后：第一条语句实参个数不符，第二条被调未登记 → 报前者。
	expectErr(t, r, ErrArgCountMismatch, "ord1", 0, 1,
		[]Stmt{callStmt(-1, "dup", 0, 0), callStmt(-1, "ghost")})
	// 交换语句次序 → 报后者。
	expectErr(t, r, ErrUnknownCallee, "ord2", 0, 1,
		[]Stmt{callStmt(-1, "ghost"), callStmt(-1, "dup", 0, 0)})
	// 自调用不受未登记限制，但实参个数必须等于自身 k。
	expectErr(t, r, ErrArgCountMismatch, "self", 2, 2, []Stmt{callStmt(-1, "self", 0)})
	mustRegister(t, r, "selfok", 2, 2, []Stmt{callStmt(-1, "selfok", 0, 1)})
}

// TestTooManyFunctions 函数数上限 256，且名字已登记优先于超限。
func TestTooManyFunctions(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < maxFunctions; i++ {
		mustRegister(t, r, fmt.Sprintf("f%d", i), 0, 0, nil)
	}
	expectErr(t, r, ErrTooManyFunctions, "overflow", 0, 0, nil)
	expectErr(t, r, ErrDuplicateName, "f0", 0, 0, nil)
}

// TestRejectKeepsState 被拒绝的登记不改变任何状态，包括分配点计数。
func TestRejectKeepsState(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "first", 0, 1, []Stmt{newStmt(0), retStmt(0)})
	checkSites(t, r, "first", []Site{{1, ClassReturn}})

	// 各种拒绝都含 New 语句，若生效会消耗分配点编号。
	expectErr(t, r, ErrInvalidArgs, "bad1", 0, 1, []Stmt{newStmt(5)})
	expectErr(t, r, ErrDuplicateName, "first", 0, 1, []Stmt{newStmt(0)})
	expectErr(t, r, ErrUnknownCallee, "bad2", 0, 1, []Stmt{newStmt(0), callStmt(-1, "ghost")})
	mustRegister(t, r, "callee", 1, 1, []Stmt{retStmt(0)})
	expectErr(t, r, ErrArgCountMismatch, "bad3", 0, 1, []Stmt{newStmt(0), callStmt(-1, "callee", 0, 0)})

	// 下一个成功登记的分配点编号应紧接 1（callee 无 New）。
	mustRegister(t, r, "second", 0, 1, []Stmt{newStmt(0), retStmt(0)})
	checkSites(t, r, "second", []Site{{2, ClassReturn}})
}

// TestConcurrency 并发调用等价于某个串行顺序：无数据竞争，
// 分配点编号严格递增且无空洞。
func TestConcurrency(t *testing.T) {
	r := NewRegistry()
	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("f%d", i)
			if err := r.Register(name, 1, 2, []Stmt{newStmt(1), storeStmt(0, 1)}); err != nil {
				t.Errorf("Register(%q): %v", name, err)
				return
			}
			if _, _, err := r.Summary(name); err != nil {
				t.Errorf("Summary(%q): %v", name, err)
			}
			if _, err := r.Sites(name); err != nil {
				t.Errorf("Sites(%q): %v", name, err)
			}
		}(i)
	}
	wg.Wait()

	seen := make(map[int]bool)
	for i := 0; i < n; i++ {
		sites, err := r.Sites(fmt.Sprintf("f%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if len(sites) != 1 {
			t.Fatalf("f%d: %d sites, want 1", i, len(sites))
		}
		if sites[0].Class != ClassParam {
			t.Errorf("f%d: class %v, want param", i, sites[0].Class)
		}
		if seen[sites[0].ID] {
			t.Fatalf("site id %d assigned twice", sites[0].ID)
		}
		seen[sites[0].ID] = true
	}
	for id := 1; id <= n; id++ {
		if !seen[id] {
			t.Fatalf("site id %d missing: numbering has a hole", id)
		}
	}
}

// TestReplayDeterminism 相同的登记序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	seq := []struct {
		name  string
		k, v  int
		stmts []Stmt
	}{
		{"mk", 0, 1, []Stmt{newStmt(0), retStmt(0)}},
		{"g", 1, 2, []Stmt{newStmt(1), storeStmt(1, 0), retStmt(1)}},
		{"h", 0, 2, []Stmt{newStmt(0), callStmt(1, "g", 0), globalStmt(1)}},
		{"r", 2, 2, []Stmt{callStmt(-1, "r", 1, 0), globalStmt(0)}},
		{"use", 0, 2, []Stmt{newStmt(0), callStmt(1, "mk"), retStmt(1)}},
	}
	run := func() *Registry {
		r := NewRegistry()
		for _, f := range seq {
			mustRegister(t, r, f.name, f.k, f.v, f.stmts)
		}
		return r
	}
	r1, r2 := run(), run()
	for _, f := range seq {
		s1, n1, _ := r1.Summary(f.name)
		s2, n2, _ := r2.Summary(f.name)
		if n1 != n2 || !summaryEqual(s1, s2) {
			t.Errorf("%s: replay mismatch: (%+v,%d) vs (%+v,%d)", f.name, s1, n1, s2, n2)
		}
		sites1, _ := r1.Sites(f.name)
		sites2, _ := r2.Sites(f.name)
		if len(sites1) != len(sites2) {
			t.Fatalf("%s: site count mismatch", f.name)
		}
		for i := range sites1 {
			if sites1[i] != sites2[i] {
				t.Errorf("%s: site %d replay mismatch: %v vs %v", f.name, i, sites1[i], sites2[i])
			}
		}
	}
}
