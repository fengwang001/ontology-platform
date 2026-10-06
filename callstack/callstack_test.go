package callstack

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// ---- 构造辅助 ----

func ik(v int64) Expr                      { return &Int{Value: v} }
func vk(name string) Expr                  { return &Var{Name: name} }
func addk(a, b Expr) Expr                  { return &Add{Left: a, Right: b} }
func callk(name string, args ...Expr) Expr { return &Call{Name: name, Args: args} }
func ifk(c, t, e Expr) Expr                { return &If{Cond: c, Then: t, Else: e} }
func letk(name string, bound, body Expr) Expr {
	return &Let{Name: name, Bound: bound, Body: body}
}
func seqk(items ...Expr) Expr { return &Seq{Items: items} }
func throwk(e Expr) Expr      { return &Throw{Payload: e} }
func tryk(prot Expr, caught string, handler Expr) Expr {
	return &Try{Protected: prot, CaughtName: caught, Handler: handler}
}

func errOf(_ int64, err error) *Error { return AsError(err) }

type memLogger struct{ buf bytes.Buffer }

func (m *memLogger) Logf(format string, args ...any) {
	m.buf.WriteString(strings.TrimSpace(fmt.Sprintf(format, args...)) + "\n")
}

func tailFlag(fn *Function, name string) bool {
	found := false
	var walk func(Expr)
	walk = func(e Expr) {
		switch x := e.(type) {
		case *Call:
			if x.Name == name {
				found = x.Tail
			}
			for _, a := range x.Args {
				walk(a)
			}
		case *Add:
			walk(x.Left)
			walk(x.Right)
		case *If:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *Let:
			walk(x.Bound)
			walk(x.Body)
		case *Seq:
			for _, it := range x.Items {
				walk(it)
			}
		case *Throw:
			walk(x.Payload)
		case *Try:
			walk(x.Protected)
			walk(x.Handler)
		}
	}
	walk(fn.Body)
	return found
}

// ---- 1. 尾位置全部语法形态与反例 ----

func TestTailPositionSyntax(t *testing.T) {
	cases := []struct {
		name string
		body Expr
		want bool
	}{
		{"body last call", callk("g"), true},
		{"if branches", ifk(ik(1), callk("g"), callk("h")), true},
		{"if condition", ifk(callk("c"), ik(1), ik(2)), false},
		{"seq last", seqk(ik(1), callk("g")), true},
		{"seq non-last", seqk(callk("g"), ik(1)), false},
		{"argument position", callk("h", callk("g")), false},
		{"feeds add", addk(callk("g"), ik(1)), false},
		{"let bound", letk("x", callk("g"), vk("x")), false},
		{"let body", letk("x", ik(1), callk("g")), true},
		{"protected region", tryk(callk("g"), "e", ik(0)), false},
		{"protected region last", tryk(seqk(ik(1), callk("g")), "e", ik(0)), false},
		{"handler last", tryk(throwk(ik(7)), "e", callk("g")), true},
		{"throw payload", throwk(callk("g")), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn := NewFunction("f", nil, tc.body)
			if got := tailFlag(fn, "g"); got != tc.want {
				t.Fatalf("tail = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTailPositionConstantOverhead(t *testing.T) {
	small := NewFunction("s", nil, callk("g"))
	bigBody := Expr(callk("g"))
	for k := 0; k < 10000; k++ {
		bigBody = seqk(ik(1), bigBody)
	}
	big := NewFunction("b", nil, bigBody)
	if !tailFlag(small, "g") || !tailFlag(big, "g") {
		t.Fatal("final calls must be tail regardless of body size")
	}
}

// ---- 程序集合：尾递归 loop 与非尾递归 down ----

func loopFuncs() []*Function {
	loop := NewFunction("loop", []string{"n"},
		ifk(vk("n"),
			callk("loop", addk(vk("n"), ik(-1))),
			ik(42)))
	return []*Function{loop}
}

func downFuncs() []*Function {
	down := NewFunction("down", []string{"n"},
		ifk(vk("n"),
			addk(callk("down", addk(vk("n"), ik(-1))), ik(0)),
			ik(0)))
	return []*Function{down}
}

// ---- 2. 尾复用不加深栈 ----

func TestTailReuseKeepsDepthOne(t *testing.T) {
	lg := &memLogger{}
	in := New(Config{MaxDepth: 2, Functions: loopFuncs(), Logger: lg})
	var snaps []Stats
	in.SetBoundaryHook(func(s Stats, _ []FrameInfo) { snaps = append(snaps, s) })

	value, err := in.Call("loop", []int64{500})
	if err != nil || value != 42 {
		t.Fatalf("value=%d err=%v, want 42/nil", value, err)
	}
	st := in.Stats()
	if st.TailReuses != 500 || st.MaxDepth != 1 || st.Depth != 0 || st.Slots != 0 {
		t.Fatalf("stats=%+v, want reuses=500 maxDepth=1 drained", st)
	}
	for i, s := range snaps {
		if s.Depth != 1 {
			t.Fatalf("snapshot %d depth=%d, want 1", i, s.Depth)
		}
	}
}

// ---- 3. 折叠计数累加与归零 ----

func TestFoldAccumulatesAndResets(t *testing.T) {
	// h: 先非尾调用 k（folded 归零），再尾调用 done。
	h := NewFunction("h", nil, seqk(addk(callk("k"), ik(0)), callk("done")))
	k := NewFunction("k", nil, ik(1))
	g := NewFunction("g", nil, callk("h"))
	f := NewFunction("f", nil, callk("g"))
	done := NewFunction("done", nil, ik(99))
	in := New(Config{MaxDepth: 10, Functions: []*Function{f, g, h, k, done}})

	var atK, atDone []FrameInfo
	in.SetBoundaryHook(func(s Stats, tr []FrameInfo) {
		switch s.Depth {
		case 2:
			atK = append([]FrameInfo(nil), tr...)
		case 1:
			atDone = append([]FrameInfo(nil), tr...)
		}
	})

	value, err := in.Call("f", nil)
	if err != nil || value != 99 {
		t.Fatalf("value=%d err=%v", value, err)
	}
	if len(atK) != 2 || atK[0].Function != "h" || atK[0].Folded != 2 ||
		atK[1].Function != "k" || atK[1].Folded != 0 {
		t.Fatalf("trace at k = %+v, want [h:2 k:0]", atK)
	}
	if len(atDone) != 1 || atDone[0].Function != "done" || atDone[0].Folded != 3 {
		t.Fatalf("trace at done = %+v, want [done:3]", atDone)
	}
}

// ---- 4. 深度/配额边界恰好取等 ----

func TestDepthBoundaryEqual(t *testing.T) {
	fns := downFuncs()
	in := New(Config{MaxDepth: 3, Functions: fns})
	if _, err := in.Call("down", []int64{2}); err != nil {
		t.Fatalf("equal-limit must succeed (3 frames), got %v", err)
	}
	in2 := New(Config{MaxDepth: 3, Functions: fns})
	if e := errOf(in2.Call("down", []int64{3})); e == nil || e.Kind != ErrDepth {
		t.Fatalf("want ErrDepth on 4th frame, got %v", e)
	}
	if st := in2.Stats(); st.Depth != 0 || st.Slots != 0 {
		t.Fatalf("rejected call changed state: %+v", st)
	}
}

func bigSlotFuncs() []*Function {
	callee := NewFunction("callee", []string{"a"}, vk("a"))
	caller := NewFunction("caller", []string{"x"},
		letk("p", ik(1),
			letk("q", ik(2),
				addk(callk("callee", ik(5)), vk("p")))))
	return []*Function{callee, caller}
}

func TestQuotaBoundaryEqualAndRejection(t *testing.T) {
	fns := bigSlotFuncs()
	in := New(Config{MaxSlots: 5, Functions: fns})
	if v, err := in.Call("caller", []int64{0}); err != nil || v != 6 {
		t.Fatalf("equal quota want 6, got %d %v", v, err)
	}

	in2 := New(Config{MaxSlots: 3, Functions: fns})
	var mid []FrameInfo
	in2.SetBoundaryHook(func(_ Stats, tr []FrameInfo) { mid = tr })
	if e := errOf(in2.Call("caller", []int64{0})); e == nil || e.Kind != ErrQuota {
		t.Fatalf("want ErrQuota, got %v", e)
	}
	if len(mid) != 1 || mid[0].Function != "caller" {
		t.Fatalf("caller frame must remain intact, got %+v", mid)
	}
	if st := in2.Stats(); st.Depth != 0 || st.Slots != 0 {
		t.Fatalf("state leaked: %+v", st)
	}
}

// ---- 5. 尾复用事务失败，原帧完好 ----

func TestTailReuseAtomicOnQuotaFailure(t *testing.T) {
	big := NewFunction("big", []string{"a"},
		letk("p", ik(1), letk("q", ik(2), addk(vk("p"), vk("q")))))
	small := NewFunction("small", nil, callk("big", ik(1)))

	in := New(Config{MaxSlots: 2, MaxDepth: 5, Functions: []*Function{big, small}})
	var mid []FrameInfo
	in.SetBoundaryHook(func(_ Stats, tr []FrameInfo) { mid = tr })
	if e := errOf(in.Call("small", nil)); e == nil || e.Kind != ErrQuota {
		t.Fatalf("want ErrQuota, got %v", e)
	}
	if len(mid) != 1 || mid[0].Function != "small" || mid[0].Folded != 0 {
		t.Fatalf("original small frame must survive, got %+v", mid)
	}

	inOK := New(Config{MaxSlots: 3, MaxDepth: 5, Functions: []*Function{big, small}})
	if v, err := inOK.Call("small", nil); err != nil || v != 3 {
		t.Fatalf("with room, small->big should yield 3, got %d %v", v, err)
	}
}

// ---- 6. 错误优先级 ----

func TestErrorPriority(t *testing.T) {
	fns := downFuncs()

	in := New(Config{MaxDepth: 1, MaxSlots: 1, Functions: fns})
	if e := errOf(in.Call("missing", []int64{1, 2, 3})); e == nil || e.Kind != ErrUndefinedFunction {
		t.Fatalf("undefined must outrank arity, got %v", e)
	}

	// 深度与配额同时成立：深度优先。entry 占满第 1 帧后非尾调用 down。
	entry := NewFunction("entry", nil, addk(callk("down", ik(0)), ik(0)))
	in2 := New(Config{MaxDepth: 1, MaxSlots: 1, Functions: append(fns, entry)})
	if e := errOf(in2.Call("entry", nil)); e == nil || e.Kind != ErrDepth {
		t.Fatalf("depth must outrank quota, got %v", e)
	}

	// 参数错误优先于资源错误。
	badArity := NewFunction("bad", nil, callk("down", ik(1), ik(2)))
	in3 := New(Config{MaxDepth: 1, MaxSlots: 1, Functions: append(fns, badArity)})
	if e := errOf(in3.Call("bad", nil)); e == nil || e.Kind != ErrArity {
		t.Fatalf("arity must outrank resource errors, got %v", e)
	}
}

// ---- 7. 异常跨越被省略帧；区域随帧身份失效 ----

func TestExceptionAcrossOmittedFramesCaught(t *testing.T) {
	h := NewFunction("h", nil, tryk(throwk(ik(77)), "e", addk(vk("e"), ik(1))))
	g := NewFunction("g", nil, callk("h"))
	f := NewFunction("f", nil, callk("g"))
	in := New(Config{MaxDepth: 10, Functions: []*Function{f, g, h}})
	if v, err := in.Call("f", nil); err != nil || v != 78 {
		t.Fatalf("surviving frame handler must catch, got %d %v", v, err)
	}
}

func TestUnhandledTraceCarriesFoldCount(t *testing.T) {
	h2 := NewFunction("h2", nil, throwk(ik(5)))
	g2 := NewFunction("g2", nil, callk("h2"))
	f2 := NewFunction("f2", nil, callk("g2"))
	in := New(Config{MaxDepth: 10, Functions: []*Function{f2, g2, h2}})
	e := errOf(in.Call("f2", nil))
	if e == nil || e.Kind != ErrUnhandled || e.Payload != 5 {
		t.Fatalf("want unhandled payload 5, got %v", e)
	}
	if len(e.Trace) != 1 || e.Trace[0].Function != "h2" || e.Trace[0].Folded != 2 {
		t.Fatalf("trace=%+v, want single h2 with folded=2", e.Trace)
	}
}

func TestRegionOfReplacedFrameCannotHandle(t *testing.T) {
	// a 不设 Try，尾调用 b；b 用自己帧的 Try 捕获。此用例验证区域按
	// frameID 绑定：a 被替换后其任何残留区域都不可能命中 b 的异常。
	b := NewFunction("b", nil, tryk(throwk(ik(9)), "e", vk("e")))
	a := NewFunction("a", nil, callk("b"))
	in := New(Config{Functions: []*Function{a, b}})
	if v, err := in.Call("a", nil); err != nil || v != 9 {
		t.Fatalf("b handler catches in its own frame, got %d %v", v, err)
	}
}

// ---- 8. 朴素模型随机对照 ----

type genProgT struct {
	funcs map[string]*Function
	list  []*Function
}

func genProg(r *rand.Rand, n int) *genProgT {
	names := make([]string, n)
	for i := range names {
		names[i] = "fn" + fmt.Sprint(i)
	}
	p := &genProgT{funcs: map[string]*Function{}}
	for idx, name := range names {
		fn := NewFunction(name, []string{"n"}, genBody(r, names, idx, 3))
		p.funcs[name] = fn
		p.list = append(p.list, fn)
	}
	return p
}

func genBody(r *rand.Rand, names []string, self, fuel int) Expr {
	if fuel <= 0 || r.Intn(3) == 0 {
		if r.Intn(2) == 0 {
			return ik(int64(r.Intn(7)))
		}
		return vk("n")
	}
	switch r.Intn(7) {
	case 0:
		return addk(genBody(r, names, self, fuel-1), genBody(r, names, self, fuel-1))
	case 1:
		return ifk(vk("n"),
			callk(names[self], addk(vk("n"), ik(-1))),
			ik(int64(self)))
	case 2:
		callee := names[r.Intn(len(names))]
		return ifk(vk("n"),
			addk(callk(callee, addk(vk("n"), ik(-1))), ik(int64(self))),
			ik(int64(self)))
	case 3:
		return letk("x", genBody(r, names, self, fuel-1), genBody(r, names, self, fuel-1))
	case 4:
		return seqk(ik(int64(self)), genBody(r, names, self, fuel-1))
	case 5:
		return tryk(throwk(ik(3)), "e", addk(vk("e"), genBody(r, names, self, fuel-1)))
	default:
		return ifk(vk("n"), genBody(r, names, self, fuel-1), genBody(r, names, self, fuel-1))
	}
}

func TestDifferentialAgainstNaive(t *testing.T) {
	r := rand.New(rand.NewSource(20261006))
	for iter := 0; iter < 400; iter++ {
		p := genProg(r, 1+r.Intn(4))
		start := p.list[r.Intn(len(p.list))].Name
		arg := int64(r.Intn(6))
		limit := 1 + r.Intn(6)

		nv, nerr := naiveCall(p.funcs, limit, 0, start, []int64{arg})
		in := New(Config{MaxDepth: limit, Functions: p.list})
		rv, rerr := in.Call(start, []int64{arg})

		if nerr == nil {
			if rerr != nil || rv != nv {
				t.Fatalf("iter %d: naive=%d real=%d,%v", iter, nv, rv, rerr)
			}
			continue
		}
		if rerr != nil {
			k := errOf(0, rerr).Kind
			if k != ErrDepth && k != ErrQuota {
				t.Fatalf("iter %d: unexpected real %v (naive %v)", iter, rerr, nerr)
			}
		}
	}
}

// ---- 9. 并发一致读取与多实例隔离 ----

func TestConcurrentReadsAreConsistent(t *testing.T) {
	in := New(Config{MaxDepth: 4, Functions: loopFuncs()})
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
					s := in.Stats()
					tr := in.Backtrace()
					if s.Depth != len(tr) {
						t.Errorf("torn: depth=%d trace=%d", s.Depth, len(tr))
						return
					}
					if (s.Depth == 0) != (s.Slots == 0) {
						t.Errorf("torn: depth=%d slots=%d", s.Depth, s.Slots)
						return
					}
				}
			}
		}()
	}
	for k := 0; k < 40; k++ {
		if _, err := in.Call("loop", []int64{int64(k % 4)}); err != nil {
			t.Fatalf("call: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}

func TestInstancesAreIsolated(t *testing.T) {
	a := New(Config{MaxDepth: 2, Functions: loopFuncs()})
	b := New(Config{MaxDepth: 2, Functions: loopFuncs()})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = a.Call("loop", []int64{100}) }()
	go func() { defer wg.Done(); _, _ = b.Call("loop", []int64{100}) }()
	wg.Wait()
	if sa, sb := a.Stats(), b.Stats(); sa.TailReuses != 100 || sb.TailReuses != 100 {
		t.Fatalf("isolated counters: %+v %+v", sa, sb)
	}
}

// ---- 10. 关闭实例与日志 ----

func TestClosedInstance(t *testing.T) {
	in := New(Config{Functions: loopFuncs()})
	in.Close()
	if e := errOf(in.Call("loop", []int64{1})); e == nil || e.Kind != ErrClosed {
		t.Fatalf("want ErrClosed, got %v", e)
	}
	if s := in.Stats(); s != (Stats{}) {
		t.Fatalf("stats must remain zero, got %+v", s)
	}
}

func TestLoggerCoversInputOutputAndBasis(t *testing.T) {
	lg := &memLogger{}
	in := New(Config{MaxDepth: 3, Functions: loopFuncs(), Logger: lg})
	if _, err := in.Call("loop", []int64{2}); err != nil {
		t.Fatal(err)
	}
	text := lg.buf.String()
	for _, want := range []string{
		"input call loop[2]",
		"output 42",
		"basis syntactic tail",
		"tail reuse loop -> loop",
		"folded=2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %q\n%s", want, text)
		}
	}
}

// ---- 11. 非尾调用链上异常逐帧弹出（多帧回溯） ----

func TestUnhandledTraceAcrossNonTailFrames(t *testing.T) {
	// a 非尾调用 b，b 非尾调用 c，c 抛出：三帧都应在传播路径上。
	c := NewFunction("c", nil, throwk(ik(1)))
	b := NewFunction("b", nil, addk(callk("c"), ik(0)))
	a := NewFunction("a", nil, addk(callk("b"), ik(0)))
	in := New(Config{MaxDepth: 10, Functions: []*Function{a, b, c}})
	e := errOf(in.Call("a", nil))
	if e == nil || e.Kind != ErrUnhandled {
		t.Fatalf("want unhandled, got %v", e)
	}
	want := []FrameInfo{
		{Function: "a", Folded: 0},
		{Function: "b", Folded: 0},
		{Function: "c", Folded: 0},
	}
	if len(e.Trace) != 3 {
		t.Fatalf("trace=%+v want 3 frames", e.Trace)
	}
	for i := range want {
		if e.Trace[i] != want[i] {
			t.Fatalf("trace[%d]=%+v want %+v; full=%+v", i, e.Trace[i], want[i], e.Trace)
		}
	}
	// 混合：a 尾调用 b，b 非尾调用 c，c 抛出。物理路径为 [b(folded=1), c(0)]。
	c2 := NewFunction("c2", nil, throwk(ik(2)))
	b2 := NewFunction("b2", nil, addk(callk("c2"), ik(0)))
	a2 := NewFunction("a2", nil, callk("b2"))
	in2 := New(Config{MaxDepth: 10, Functions: []*Function{a2, b2, c2}})
	e2 := errOf(in2.Call("a2", nil))
	want2 := []FrameInfo{
		{Function: "b2", Folded: 1},
		{Function: "c2", Folded: 0},
	}
	if len(e2.Trace) != 2 || e2.Trace[0] != want2[0] || e2.Trace[1] != want2[1] {
		t.Fatalf("mixed trace=%+v want %+v", e2.Trace, want2)
	}
}
