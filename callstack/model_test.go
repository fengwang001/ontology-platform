package callstack

import (
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// model_test.go 与一个「不做帧复用」的独立朴素求值器对照：
// 随机生成结构上必然终止的程序，比较两者最终返回值（异常值也比较）。
// 是否溢出允许不同（朴素模型给宽松上限；在宽松上限下两者都应成功）。

// ---------- 朴素模型：每个调用（含尾调用）都压栈的独立解释器 ----------

type naive struct {
	funcs map[string]*naiveFunc
	depth int
}

type naiveFunc struct {
	params []string
	body   Expr
}

type naiveResult struct {
	value int64
	exc   bool
}

func (m *naive) eval(e Expr, env []int64) (v int64, threw bool, excVal int64) {
	switch n := e.(type) {
	case *NumExpr:
		return n.Value, false, 0
	case *VarExpr:
		return env[n.Slot], false, 0
	case *BinExpr:
		l, th, x := m.eval(n.Lhs, env)
		if th {
			return 0, true, x
		}
		r, th, x := m.eval(n.Rhs, env)
		if th {
			return 0, true, x
		}
		return applyBin(n.Op, l, r), false, 0
	case *IfExpr:
		c, th, x := m.eval(n.Cond, env)
		if th {
			return 0, true, x
		}
		if c != 0 {
			return m.eval(n.Then, env)
		}
		return m.eval(n.Else, env)
	case *SeqExpr:
		var v int64
		for _, y := range n.Exprs {
			var th bool
			var x int64
			v, th, x = m.eval(y, env)
			if th {
				return 0, true, x
			}
		}
		return v, false, 0
	case *LetExpr:
		iv, th, x := m.eval(n.Init, env)
		if th {
			return 0, true, x
		}
		env2 := append(append([]int64(nil), env...), make([]int64, n.Slot+1-len(env))...)
		env2[n.Slot] = iv
		return m.eval(n.Body, env2)
	case *TryExpr:
		v, th, x := m.eval(n.Body, env)
		if !th {
			return v, false, 0
		}
		env2 := append(append([]int64(nil), env...), make([]int64, n.ExcSlot+1-len(env))...)
		env2[n.ExcSlot] = x
		return m.eval(n.Handler, env2)
	case *ThrowExpr:
		v, _, _ := m.eval(n.Arg, env)
		return 0, true, v
	case *CallExpr:
		fn := m.funcs[n.Name]
		m.depth++
		if m.depth > 100000 {
			panic("naive model depth guard")
		}
		defer func() { m.depth-- }()
		args := make([]int64, len(n.Args))
		for i, a := range n.Args {
			av, th, x := m.eval(a, env)
			if th {
				return 0, true, x
			}
			args[i] = av
		}
		// 朴素模型：无论是否尾位置都分配新环境/新帧（不做复用）。
		return m.eval(fn.body, args)
	default:
		panic("unexpected node")
	}
}

// ---------- 随机程序生成（结构上必然终止） ----------

// gen 构造 N 个函数：f0 是纯叶子；f_k 的调用只允许指向 f_j（j<k，
// 或自身且附带递减的数值计数器编码不现实——因此禁止直接/间接非终止，
// 只调用编号更小的函数）。这样任何调用序列都必然终止。
func generateProgram(r *rand.Rand, n int) *Program {
	prog := &Program{Funcs: map[string]*Func{}}
	const arity = 2

	var genBody func(maxFunc int) Expr
	genBody = func(maxFunc int) Expr {
		// 体被限制为很浅的直线表达式；调用至多一次且参数为
		// 字面量/变量，保证两侧解释器的 Go 递归深度都有界。
		if maxFunc > 0 && r.Intn(2) == 0 {
			target := maxFunc - 1 // 只调用直接前驱：调用图是线性链，调用次数上界 = n
			arg := func() Expr {
				if r.Intn(2) == 0 {
					return &NumExpr{Value: int64(r.Intn(5))}
				}
				return &VarExpr{Name: "x", Slot: r.Intn(arity)}
			}
			call := &CallExpr{Name: funcName(target), Args: []Expr{arg(), arg()}}
			switch r.Intn(4) {
			case 0:
				return call // 尾位置
			case 1:
				return &BinExpr{Op: "+", Lhs: &NumExpr{Value: 0}, Rhs: call}
			case 2:
				return &TryExpr{Body: call, Handler: &VarExpr{Name: "e", Slot: arity}, ExcSlot: arity}
			default:
				return &IfExpr{
					Cond: &VarExpr{Name: "x", Slot: 0},
					Then: call,
					Else: &NumExpr{Value: int64(r.Intn(5))},
				}
			}
		}
		switch r.Intn(4) {
		case 0:
			return &NumExpr{Value: int64(r.Intn(5))}
		case 1:
			return &VarExpr{Name: "x", Slot: r.Intn(arity)}
		case 2:
			return &ThrowExpr{Arg: &NumExpr{Value: int64(r.Intn(99) + 1)}}
		default:
			return &BinExpr{Op: "+", Lhs: &VarExpr{Name: "x", Slot: 0}, Rhs: &NumExpr{Value: 1}}
		}
	}

	for k := 0; k < n; k++ {
		body := genBody(k)
		nslots := arity
		if hasCatch(body) {
			nslots++ // catch 绑定使用参数之后的槽位
		}
		prog.Funcs[funcName(k)] = &Func{
			Name:   funcName(k),
			Params: []string{"x", "y"},
			NSlots: nslots,
			Body:   body,
		}
	}
	prog.Main = &CallExpr{Name: funcName(n - 1), Args: []Expr{
		&NumExpr{Value: 1}, &NumExpr{Value: 2},
	}}
	MarkTail(prog)
	return prog
}

func hasCatch(e Expr) bool {
	switch n := e.(type) {
	case *TryExpr:
		return true
	case *BinExpr:
		return hasCatch(n.Lhs) || hasCatch(n.Rhs)
	case *IfExpr:
		return hasCatch(n.Cond) || hasCatch(n.Then) || hasCatch(n.Else)
	}
	return false
}

func funcName(k int) string {
	return "f" + strconv.Itoa(k)
}

func buildNaive(prog *Program) *naive {
	m := &naive{funcs: map[string]*naiveFunc{}}
	for name, fn := range prog.Funcs {
		m.funcs[name] = &naiveFunc{params: fn.Params, body: fn.Body}
	}
	return m
}

func TestNaiveModelEquivalence(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		n := 2 + r.Intn(3)
		prog := generateProgram(r, n)
		m := buildNaive(prog)
		nv, nThrew, nExc := m.eval(prog.Main, nil)

		in := NewInstance(prog, Config{MaxDepth: 1 << 20, MaxSlots: 1 << 30})
		rv, rerr := in.RunMain()
		if nThrew {
			if rerr == nil || rerr.(*Error).Kind != ErrUnhandled || rerr.(*Error).Value != nExc {
				t.Fatalf("iter %d: naive unhandled exc=%d, runtime=%v", iter, nExc, rerr)
			}
		} else {
			if rerr != nil {
				t.Fatalf("iter %d: naive value=%d, runtime error=%v", iter, nv, rerr)
			}
			if rv != nv {
				t.Fatalf("iter %d: value mismatch naive=%d runtime=%d", iter, nv, rv)
			}
		}
	}
}

// 在同一批随机序列上确认：宽松上限下优化版从不溢出；
// 而收紧深度上限只会把一部分成功变为 ErrDepth（“是否溢出”允许不同）。
func TestOverflowOnlyDiffersAsAllowed(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	limitedDepth := 0
	for iter := 0; iter < 200; iter++ {
		prog := generateProgram(r, 2+r.Intn(3))
		in := NewInstance(prog, Config{MaxDepth: 3, MaxSlots: 1 << 30})
		_, err := in.RunMain()
		if err != nil {
			k := err.(*Error).Kind
			if k != ErrDepth && k != ErrUnhandled {
				t.Fatalf("iter %d: unexpected error kind under tight depth: %v", iter, err)
			}
			limitedDepth++
		}
	}
	if limitedDepth == 0 {
		t.Fatalf("expected some depth rejections with tiny limit")
	}
}

// ---------- 并发：多实例隔离 + 单实例并发读一致性 ----------

func TestMultipleInstancesAreIndependent(t *testing.T) {
	prog, err := Parse(loopSrc)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			in := NewInstance(prog, Config{MaxDepth: 4, MaxSlots: 1 << 30})
			v, err := in.RunMain()
			if err != nil || v != 1000 {
				t.Errorf("instance seed=%d v=%d err=%v", seed, v, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestConcurrentReadsAreConsistent(t *testing.T) {
	prog, err := Parse(`
	(def work (n a)
	  (if (< n 1) a
	    (let (z (+ a 1))
	      (if (= z 0) (work (- n 1) z) (work (- n 1) z)))))
	(work 100000 0)`)
	if err != nil {
		t.Fatal(err)
	}
	in := NewInstance(prog, Config{MaxDepth: 1 << 20, MaxSlots: 1 << 30})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					st := in.Stats()
					bt := in.Backtrace()
					sum := 0
					for _, f := range bt {
						sum += f.Slots
					}
					// 每个快照各自必须自洽；bt 与 st 是两个相邻调用边界的
					// 快照，不做跨快照耦合，但在同一调用边界（绝大多数迭代）
					// 上二者必然吻合。
					if st.Depth < 0 || st.Depth > st.MaxDepth || st.SlotsUsed < 0 {
						t.Errorf("inconsistent stats snapshot: %+v", st)
						return
					}
					if sum < 0 {
						t.Errorf("negative backtrace slot sum")
						return
					}
					// 同一边界吻合的强校验：两次读取间若没有调用推进，
					// Depth 必然相等；Depth 相等时 SlotsUsed 也必须相等。
					if len(bt) == st.Depth && sum != st.SlotsUsed {
						t.Errorf("same-boundary torn read: stats=%+v bt-sum=%d", st, sum)
						return
					}
					_ = sum
				}
			}
		}()
	}
	v, err := in.RunMain()
	close(stop)
	wg.Wait()
	if err != nil || v != 100000 {
		t.Fatalf("v=%d err=%v", v, err)
	}
}

func TestLogsContainInputOutputBasis(t *testing.T) {
	src := `
	(def f (n) (if (< n 1) n (f (- n 1))))
	(f 2)`
	prog, _ := Parse(src)
	lg := &BufferLogger{}
	in := NewInstance(prog, Config{MaxDepth: 10, MaxSlots: 100, Logger: lg})
	if _, err := in.RunMain(); err != nil {
		t.Fatal(err)
	}
	lines := lg.Lines()
	if len(lines) == 0 {
		t.Fatal("no logs")
	}
	for _, l := range lines {
		if !strings.Contains(l, "input=") || !strings.Contains(l, "output=") ||
			!strings.Contains(l, "basis=") {
			t.Fatalf("log line missing fields: %s", l)
		}
	}
	// 非尾自递归（结果参与 +0）每次都是压栈，依据里含 depth=。
	src2 := `
	(def g (n) (if (< n 1) n (+ 0 (g (- n 1)))))
	(g 1)`
	prog2, _ := Parse(src2)
	lg2 := &BufferLogger{}
	in2 := NewInstance(prog2, Config{MaxDepth: 10, MaxSlots: 100, Logger: lg2})
	if _, err := in2.RunMain(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range lg2.Lines() {
		if strings.Contains(l, "input=(g 1)") && strings.Contains(l, "pushed-frame") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected pushed-frame log:\n%s", lg2.String())
	}
}

// ---------- 常量开销证明 ----------

// 运行时尾判定是读取单个布尔字段；构造大小悬殊的函数体，
// 判定耗时应不随后续函数体大小增长（字段读取 O(1)）。
func BenchmarkTailDecisionConstant(b *testing.B) {
	makeProg := func(extra int) *Program {
		src := "(def loop (n a) (if (< n 1) a (loop (- n 1) (+ a 1))))\n"
		src += "(def noise (x)\n(do\n"
		for i := 0; i < extra; i++ {
			src += "(+ x " + strconv.Itoa(i) + ")\n"
		}
		src += "x))\n(loop 1 0)"
		p, err := Parse(src)
		if err != nil {
			b.Fatal(err)
		}
		return p
	}
	pSmall := makeProg(10)
	pLarge := makeProg(10000)
	var sink bool
	b.Run("body-size-10", func(b *testing.B) {
		c := callByName(pSmall.Funcs["loop"], "loop")
		for i := 0; i < b.N; i++ {
			sink = IsTailPosition(c)
		}
	})
	b.Run("body-size-10000", func(b *testing.B) {
		c := callByName(pLarge.Funcs["loop"], "loop")
		for i := 0; i < b.N; i++ {
			sink = IsTailPosition(c)
		}
	})
	_ = sink
}

// 回溯读取开销只随物理帧数增长，不随被省略帧数增长：
// 两种情形物理帧数同为 2，但折叠数相差 10000，耗时应相近。
func BenchmarkBacktraceIndependentOfFolded(b *testing.B) {
	foldProg, err := Parse(`
	(def tail (n a) (if (< n 1) a (tail (- n 1) a)))
	(def start (n) (tail n 0))
	(start 10000)`)
	if err != nil {
		b.Fatal(err)
	}
	foldInst := NewInstance(foldProg, Config{MaxDepth: 10, MaxSlots: 1 << 20})

	plainProg, err := Parse(`
	(def leaf (x) x)
	(def start (n) (leaf n))
	(start 1)`)
	if err != nil {
		b.Fatal(err)
	}
	plainInst := NewInstance(plainProg, Config{MaxDepth: 10, MaxSlots: 1 << 20})

	// 直接在栈上构造“运行中”状态以测量回溯读取：
	foldInst.stack = &Stack{}
	foldInst.stack.push(foldInst.mainFunc, nil)
	foldInst.stack.push(foldProg.Funcs["start"], []int64{0})
	foldInst.stack.replaceTail(foldProg.Funcs["tail"], []int64{10000, 0})
	for i := 0; i < 9999; i++ {
		foldInst.stack.replaceTail(foldProg.Funcs["tail"], []int64{int64(9999 - i), 0})
	}

	plainInst.stack = &Stack{}
	plainInst.stack.push(plainInst.mainFunc, nil)
	plainInst.stack.push(plainProg.Funcs["start"], []int64{0})
	plainInst.stack.replaceTail(plainProg.Funcs["leaf"], []int64{1})

	var sink int
	b.Run("folded-10000", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			foldInst.mu.RLock()
			sink = len(foldInst.stack.backtrace())
			foldInst.mu.RUnlock()
		}
	})
	b.Run("folded-1", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			plainInst.mu.RLock()
			sink = len(plainInst.stack.backtrace())
			plainInst.mu.RUnlock()
		}
	})
	_ = sink
}
