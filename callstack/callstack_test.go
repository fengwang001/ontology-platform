package callstack

import (
	"strings"
	"testing"
)

func runOK(t *testing.T, src string, cfg Config) (int64, *Instance) {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	in := NewInstance(prog, cfg)
	v, err := in.RunMain()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return v, in
}

func runErr(t *testing.T, src string, cfg Config) *Error {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	in := NewInstance(prog, cfg)
	_, err = in.RunMain()
	if err == nil {
		t.Fatalf("expected error, got success")
	}
	ce, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *callstack.Error, got %T", err)
	}
	return ce
}

func findCalls(e Expr) []*CallExpr {
	var out []*CallExpr
	var walk func(Expr)
	walk = func(x Expr) {
		switch n := x.(type) {
		case *CallExpr:
			out = append(out, n)
			for _, a := range n.Args {
				walk(a)
			}
		case *IfExpr:
			walk(n.Cond)
			walk(n.Then)
			walk(n.Else)
		case *BinExpr:
			walk(n.Lhs)
			walk(n.Rhs)
		case *SeqExpr:
			for _, y := range n.Exprs {
				walk(y)
			}
		case *LetExpr:
			walk(n.Init)
			walk(n.Body)
		case *TryExpr:
			walk(n.Body)
			walk(n.Handler)
		case *ThrowExpr:
			walk(n.Arg)
		}
	}
	walk(e)
	return out
}

func callByName(f *Func, name string) *CallExpr {
	for _, c := range findCalls(f.Body) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestTailPositionForms(t *testing.T) {
	src := `
	(def body (x) (f x))
	(def branches (x) (if x (a x) (b x)))
	(def seq (x) (do 1 2 (g x)))
	(def letbody (x) (let (y 3) (h y)))
	(def nested (x) (if x (do 1 (i x)) (let (z 2) (j z))))
	0`
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := []struct{ fn, call, reason string }{
		{"body", "f", reasonBody},
		{"branches", "a", reasonIfBranch},
		{"branches", "b", reasonIfBranch},
		{"seq", "g", reasonSeqLast},
		{"letbody", "h", reasonLetBody},
		{"nested", "i", reasonSeqLast},
		{"nested", "j", reasonLetBody},
	}
	for _, tc := range cases {
		c := callByName(prog.Funcs[tc.fn], tc.call)
		if c == nil {
			t.Fatalf("%s: call %s not found", tc.fn, tc.call)
		}
		if !c.Tail {
			t.Errorf("%s/%s: expected tail", tc.fn, tc.call)
		}
		if c.TailReason != tc.reason {
			t.Errorf("%s/%s: reason=%q want %q", tc.fn, tc.call, c.TailReason, tc.reason)
		}
	}
}

func TestTailPositionCounterExamples(t *testing.T) {
	src := `
	(def asarg (x) (k (f x)))
	(def bin (x) (+ 1 (f x)))
	(def ifcond (x) (if (f x) 1 2))
	(def seqitem (x) (do (f x) 2))
	(def letinit (x) (let (y (f x)) y))
	(def protected (x) (try (f x) (catch e e)))
	(def protectedLast (x) (try (do 1 (f x)) (catch e e)))
	(def handlerPos (x) (try 1 (catch e (f e))))
	(def throwarg (x) (throw (f x)))
	0`
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := []struct{ fn, reason string }{
		{"asarg", reasonArg},
		{"bin", reasonBin},
		{"ifcond", reasonIfCond},
		{"seqitem", reasonSeqItem},
		{"letinit", reasonLetInit},
		{"protected", reasonProtected},
		{"protectedLast", reasonProtected},
		{"handlerPos", reasonHandler},
		{"throwarg", reasonThrowArg},
	}
	for _, tc := range cases {
		c := callByName(prog.Funcs[tc.fn], "f")
		if c == nil {
			t.Fatalf("%s: call f not found", tc.fn)
		}
		if c.Tail {
			t.Errorf("%s: expected non-tail call f", tc.fn)
		}
		if c.TailReason != tc.reason {
			t.Errorf("%s: reason=%q want %q", tc.fn, c.TailReason, tc.reason)
		}
	}
}

const loopSrc = `
(def loop (n acc) (if (< n 1) acc (loop (- n 1) (+ acc 1))))
(loop 1000 0)`

func TestTailReuseDepthAndFold(t *testing.T) {
	v, in := runOK(t, loopSrc, Config{MaxDepth: 100, MaxSlots: 1 << 30})
	if v != 1000 {
		t.Fatalf("value=%d want 1000", v)
	}
	st := in.Stats()
	if st.Depth != 0 {
		t.Fatalf("depth after run=%d want 0", st.Depth)
	}
	if st.MaxDepth != 2 {
		t.Fatalf("max depth=%d want 2 (main + loop); loop self-calls reuse", st.MaxDepth)
	}
	if st.Reuses != 1000 {
		t.Fatalf("reuses=%d want 1000", st.Reuses)
	}
}

// TestFoldInBacktrace 直接在深层调用存活时读取回溯，验证折叠计数。
func TestFoldInBacktrace(t *testing.T) {
	// t1 尾调用 t2，t2 尾调用 t3，t3 再非尾调用 probe；
	// probe 执行时读取自身回溯（通过给 probe 传入会失败的调用无法观察，
	// 故采用「probe 非尾调用自身前」的统计：借助一个故意未定义调用
	// 在错误的回溯中观察折叠计数）。
	src := `
	(def t3 (x) (nope x))
	(def t2 (x) (t3 x))
	(def t1 (x) (t2 x))
	(t1 7)`
	e := runErr(t, src, Config{MaxDepth: 100, MaxSlots: 1 << 20})
	if e.Kind != ErrUndefined {
		t.Fatalf("kind=%v want ErrUndefined", e.Kind)
	}
	// nope 在参数已求值后查找失败；此时帧为 main(0) <- t1 fold 0,
	// 被 t2、t3 连续尾替换，栈顶 t3 的 FoldedBefore=2；main 仍在。
	bt := e.Backtrace
	if len(bt) != 2 {
		t.Fatalf("backtrace=%+v", bt)
	}
	if bt[0].Func != "t3" || bt[0].FoldedBefore != 2 {
		t.Fatalf("inner frame=%+v want t3 folded=2", bt[0])
	}
	if bt[1].Func != "<main>" || bt[1].FoldedBefore != 0 {
		t.Fatalf("outer frame=%+v want <main> folded=0", bt[1])
	}
}

// TestFoldResetOnNonTail：非尾调用之后折叠数归零。
func TestFoldResetOnNonTail(t *testing.T) {
	// t1 尾调 t2(fold+1)；t2 非尾调用 probe；probe 尾调 p2(fold+1)；
	// p2 引用未定义函数，错误回溯中：p2 folded=1, probe folded=0,
	// t2 folded=1, main folded=0。
	src := `
	(def p2 (x) (zzz x))
	(def probe (x) (+ 0 (p2 x)))
	(def t2 (x) (+ 0 (probe x)))
	(def t1 (x) (t2 x))
	(t1 1)`
	e := runErr(t, src, Config{MaxDepth: 100, MaxSlots: 1 << 20})
	if e.Kind != ErrUndefined {
		t.Fatalf("kind=%v", e.Kind)
	}
	want := []struct {
		name string
		fold int
	}{
		{"p2", 0}, {"probe", 0}, {"t2", 1}, {"<main>", 0},
	}
	if len(e.Backtrace) != len(want) {
		t.Fatalf("backtrace=%+v", e.Backtrace)
	}
	for i, w := range want {
		if e.Backtrace[i].Func != w.name || e.Backtrace[i].FoldedBefore != w.fold {
			t.Fatalf("frame %d=%+v want %s fold=%d", i, e.Backtrace[i], w.name, w.fold)
		}
	}
}

func TestDepthBoundary(t *testing.T) {
	src := `
	(def f (n) (if (< n 1) n (+ 0 (f (- n 1)))))
	(f 2)`
	v, _ := runOK(t, src, Config{MaxDepth: 4, MaxSlots: 1 << 20})
	if v != 0 {
		t.Fatalf("equal-boundary value=%d want 0", v)
	}
	e := runErr(t, src, Config{MaxDepth: 3, MaxSlots: 1 << 20})
	if e.Kind != ErrDepth {
		t.Fatalf("kind=%v want ErrDepth", e.Kind)
	}
}

func TestQuotaBoundary(t *testing.T) {
	src := `
	(def g (n) (let (a 1) n))
	(g 42)`
	// main 0 槽 + g 2 槽。
	v, _ := runOK(t, src, Config{MaxDepth: 100, MaxSlots: 2})
	if v != 42 {
		t.Fatalf("quota equal value=%d want 42", v)
	}
	e := runErr(t, src, Config{MaxDepth: 100, MaxSlots: 1})
	if e.Kind != ErrQuota {
		t.Fatalf("kind=%v want ErrQuota", e.Kind)
	}
}

// 尾复用事务失败：日志记录“先释放再分配”的原子检查依据，
// 且复用计数为 0、最大深度只含成功帧（原帧未被替换）。
func TestTailQuotaFailureTransaction(t *testing.T) {
	src := `
	(def old (x) (big x))
	(def big (a) (let (b1 1) (let (b2 2) (let (b3 3) (let (b4 4) (let (b5 5) a))))))
	(old 9)`
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	lg := &BufferLogger{}
	in := NewInstance(prog, Config{MaxDepth: 10, MaxSlots: 3, Logger: lg})
	_, err = in.RunMain()
	ce := err.(*Error)
	if ce.Kind != ErrQuota {
		t.Fatalf("kind=%v want ErrQuota", ce.Kind)
	}
	st := in.Stats()
	if st.Reuses != 0 {
		t.Fatalf("reuses=%d want 0 on failed transaction", st.Reuses)
	}
	if st.MaxDepth != 2 {
		t.Fatalf("max depth=%d want 2 (main,old); rejected tail call left old frame intact", st.MaxDepth)
	}
	ok := false
	for _, l := range lg.Lines() {
		if strings.Contains(l, "input=(big 9)") &&
			strings.Contains(l, "rejected:quota-limit-exceeded") &&
			strings.Contains(l, "release 1 then allocate 6") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("atomic-reuse basis log missing:\n%s", lg.String())
	}
}

func TestDepthBeatsQuota(t *testing.T) {
	src := `
	(def h (n) (let (a 1) (let (b 2) (h n))))
	(h 0)`
	e := runErr(t, src, Config{MaxDepth: 1, MaxSlots: 1})
	if e.Kind != ErrDepth {
		t.Fatalf("kind=%v want ErrDepth (priority over quota)", e.Kind)
	}
}

func TestErrorPriority(t *testing.T) {
	if e := runErr(t, `(missing 1 2 3)`, Config{MaxDepth: 1, MaxSlots: 0}); e.Kind != ErrUndefined {
		t.Fatalf("undefined priority, got %v", e.Kind)
	}
	src := `
	(def q (a b) a)
	(q 1)`
	if e := runErr(t, src, Config{MaxDepth: 1, MaxSlots: 0}); e.Kind != ErrArity {
		t.Fatalf("arity priority over depth/quota, got %v", e.Kind)
	}
}

// ---------- 异常传播 ----------

func TestExceptionCaughtInSameFrame(t *testing.T) {
	src := `
	(def f (x) (try (throw 77) (catch e (+ e 1))))
	(f 0)`
	v, in := runOK(t, src, Config{MaxDepth: 10, MaxSlots: 100})
	if v != 78 {
		t.Fatalf("value=%d want 78", v)
	}
	if st := in.Stats(); st.Reuses != 0 {
		t.Fatalf("reuses=%d want 0", st.Reuses)
	}
}

// 异常跨越被尾调用省略的帧：t1->t2->t3 尾链上 t3 抛出，
// main 的 try 处理之；传播路径只含 t3（folded=2），t1/t2 不出现。
func TestExceptionPropagationSkipsFolded(t *testing.T) {
	src := `
	(def t3 (x) (throw 55))
	(def t2 (x) (t3 x))
	(def t1 (x) (t2 x))
	(try (t1 0) (catch e (* e 2)))`
	v, _ := runOK(t, src, Config{MaxDepth: 10, MaxSlots: 100})
	if v != 110 {
		t.Fatalf("handled value=%d want 110", v)
	}
}

func TestUnhandledExceptionBacktrace(t *testing.T) {
	src := `
	(def t3 (x) (throw 55))
	(def t2 (x) (t3 x))
	(def t1 (x) (t2 x))
	(t1 0)`
	e := runErr(t, src, Config{MaxDepth: 10, MaxSlots: 100})
	if e.Kind != ErrUnhandled || e.Value != 55 {
		t.Fatalf("err=%v", e)
	}
	if len(e.Backtrace) != 2 {
		t.Fatalf("backtrace len=%d want 2: %+v", len(e.Backtrace), e.Backtrace)
	}
	if e.Backtrace[0].Func != "t3" || e.Backtrace[0].FoldedBefore != 2 {
		t.Fatalf("inner=%+v want t3 folded=2", e.Backtrace[0])
	}
	if e.Backtrace[1].Func != "<main>" {
		t.Fatalf("outer=%+v", e.Backtrace[1])
	}
}

// 保护区域在尾复用后立即失效：被替换掉的 old 的 try 不再拦截异常。
func TestProtectedRegionDiesWithTailReplacement(t *testing.T) {
	src := `
	(def rep (x) (throw 9))
	(def old (x) (try (rep x) (catch e 0)))
	(old 0)`
	// rep 处于 old 保护区域之内 => 不是尾调用；故为压栈调用，
	// old 的 try 仍然生效并捕获 9 -> 0。
	v, _ := runOK(t, src, Config{MaxDepth: 10, MaxSlots: 100})
	if v != 0 {
		t.Fatalf("protected call value=%d want 0 (non-tail, region live)", v)
	}

	// 真正的尾替换：区域外的尾调用进入 rep，old 帧消失；
	// rep 再抛异常时 old 的保护区域已不存在，main 捕获。
	src2 := `
	(def rep (x) (throw 9))
	(def old (x) (rep x))
	(try (old 0) (catch e e))`
	v2, _ := runOK(t, src2, Config{MaxDepth: 10, MaxSlots: 100})
	if v2 != 9 {
		t.Fatalf("tail-replaced region value=%d want 9 (old region dead)", v2)
	}
}

func TestClosedInstance(t *testing.T) {
	prog, _ := Parse(`1`)
	in := NewInstance(prog, Config{})
	in.Close()
	if _, err := in.RunMain(); err.(*Error).Kind != ErrClosed {
		t.Fatalf("err=%v want ErrClosed", err)
	}
}

// 任何被拒绝的调用都不得改变帧栈、折叠计数、配额与统计：
// 用一次成功调用建立基线，再触发一次配额失败，断言四项统计与基线一致。
func TestRejectedCallLeavesAllStatsUntouched(t *testing.T) {
	src := `
	(def big (a) (let (b1 1) (let (b2 2) (let (b3 3) a))))
	(def mid (x) (big x))
	(def first (x) x)
	(do (first 1) (mid 7))`
	prog, _ := Parse(src)
	in := NewInstance(prog, Config{MaxDepth: 10, MaxSlots: 3})
	_, err := in.RunMain()
	if err.(*Error).Kind != ErrQuota {
		t.Fatalf("kind=%v want ErrQuota", err)
	}
	st := in.Stats()
	// 成功的只有 main + first（非尾）；first 返回后只剩 main，
	// 随后 main->mid（非尾，1槽）成功；mid 尾调 big 失败，mid 完好。
	// 运行结束整体弹出，故终态全部归零；关键是 reuses 与 maxHist。
	if st.Reuses != 0 {
		t.Fatalf("reuses=%d want 0 (failed tail reuse not counted)", st.Reuses)
	}
	if st.MaxDepth != 2 {
		t.Fatalf("maxHist=%d want 2 (main + one callee); rejected big never entered", st.MaxDepth)
	}
	if st.Depth != 0 || st.SlotsUsed != 0 {
		t.Fatalf("post-run state not clean: %+v", st)
	}
	// 拒绝错误携带的回溯必须是原帧：mid（折叠 0）而不是 big。
	e := err.(*Error)
	if len(e.Backtrace) != 2 || e.Backtrace[0].Func != "mid" {
		t.Fatalf("backtrace on rejection=%+v want innermost mid", e.Backtrace)
	}
}
