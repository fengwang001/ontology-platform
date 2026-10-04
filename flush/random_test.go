package flush

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naivePage 是朴素模拟中的页状态。
type naivePage struct {
	lsn        int64
	dirty      bool
	oldest     int64
	inFlight   bool
	snap       int64
	firstAfter int64
}

// naive 是按规格逐条写成的朴素模拟，用作 Manager 的对照模型。
type naive struct {
	limit   int
	pages   map[int]*naivePage
	order   []int // 脏页号，按 oldest 严格升序
	maxLSN  int64
	flushed int64
	out     map[int]map[int]bool
	in      map[int]map[int]bool
}

func newNaive(limit int) *naive {
	return &naive{
		limit: limit,
		pages: make(map[int]*naivePage),
		out:   make(map[int]map[int]bool),
		in:    make(map[int]map[int]bool),
	}
}

func (n *naive) page(p int) *naivePage {
	pg, ok := n.pages[p]
	if !ok {
		pg = &naivePage{}
		n.pages[p] = pg
	}
	return pg
}

func (n *naive) removeFromOrder(p int) {
	for i, q := range n.order {
		if q == p {
			n.order = append(n.order[:i], n.order[i+1:]...)
			return
		}
	}
}

func (n *naive) insertSorted(p int) {
	oldest := n.pages[p].oldest
	i := 0
	for i < len(n.order) && n.pages[n.order[i]].oldest < oldest {
		i++
	}
	n.order = append(n.order, 0)
	copy(n.order[i+1:], n.order[i:])
	n.order[i] = p
}

func (n *naive) Modify(p int, lsn int64) Code {
	if p < MinPage || p > MaxPage || lsn < MinLSN || lsn > MaxLSN {
		return CodeInvalidArgument
	}
	if lsn <= n.maxLSN {
		return CodeLSNNotAdvanced
	}
	pg := n.page(p)
	if !pg.dirty && len(n.order) >= n.limit {
		return CodeDirtyPoolFull
	}
	n.maxLSN = lsn
	pg.lsn = lsn
	switch {
	case !pg.dirty:
		pg.dirty = true
		pg.oldest = lsn
		n.order = append(n.order, p)
	case pg.inFlight:
		if pg.firstAfter == 0 {
			pg.firstAfter = lsn
		}
	}
	return 0
}

func (n *naive) SetFlushed(l int64) Code {
	if l < 0 || l > MaxLSN {
		return CodeInvalidArgument
	}
	if l < n.flushed {
		return CodeLSNNotAdvanced
	}
	n.flushed = l
	return 0
}

func (n *naive) FlushStart(p int) Code {
	if p < MinPage || p > MaxPage {
		return CodeInvalidArgument
	}
	pg := n.page(p)
	if !pg.dirty {
		return CodePageNotDirty
	}
	if pg.inFlight {
		return CodePageInFlight
	}
	if pg.lsn > n.flushed {
		return CodeLogNotFlushed
	}
	for q := range n.in[p] {
		if n.pages[q].dirty {
			return CodePredecessorDirty
		}
	}
	pg.snap = pg.lsn
	pg.inFlight = true
	return 0
}

func (n *naive) FlushDone(p int) Code {
	if p < MinPage || p > MaxPage {
		return CodeInvalidArgument
	}
	pg := n.page(p)
	if !pg.inFlight {
		return CodePageNotInFlight
	}
	pg.inFlight = false
	if pg.lsn == pg.snap {
		pg.dirty = false
		pg.firstAfter = 0
		n.removeFromOrder(p)
		for b := range n.out[p] {
			delete(n.in[b], p)
			if len(n.in[b]) == 0 {
				delete(n.in, b)
			}
		}
		delete(n.out, p)
		return 0
	}
	pg.oldest = pg.firstAfter
	pg.firstAfter = 0
	n.removeFromOrder(p)
	n.insertSorted(p)
	return 0
}

func (n *naive) AddDep(a, b int) Code {
	if a < MinPage || a > MaxPage || b < MinPage || b > MaxPage || a == b {
		return CodeInvalidArgument
	}
	if !n.page(a).dirty {
		return 0
	}
	if n.out[a][b] {
		return 0
	}
	if n.reachable(b, a) {
		return CodeDependencyCycle
	}
	if n.out[a] == nil {
		n.out[a] = make(map[int]bool)
	}
	n.out[a][b] = true
	if n.in[b] == nil {
		n.in[b] = make(map[int]bool)
	}
	n.in[b][a] = true
	return 0
}

func (n *naive) Checkpoint() int64 {
	if len(n.order) == 0 {
		return n.maxLSN + 1
	}
	return n.pages[n.order[0]].oldest
}

func (n *naive) Plan(target int64) []int {
	inPlan := make(map[int]bool)
	plan := make([]int, 0, len(n.order))
	var emit func(p int)
	emit = func(p int) {
		var preds []int
		for q := range n.in[p] {
			if qg := n.pages[q]; qg.dirty && !qg.inFlight {
				preds = append(preds, q)
			}
		}
		sort.Ints(preds)
		for _, q := range preds {
			if !inPlan[q] {
				emit(q)
			}
		}
		plan = append(plan, p)
		inPlan[p] = true
	}
	for _, p := range n.order {
		pg := n.pages[p]
		if pg.oldest < target && !pg.inFlight && !inPlan[p] {
			emit(p)
		}
	}
	return plan
}

func (n *naive) reachable(from, to int) bool {
	visited := map[int]bool{from: true}
	stack := []int{from}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x == to {
			return true
		}
		for y := range n.out[x] {
			if !visited[y] {
				visited[y] = true
				stack = append(stack, y)
			}
		}
	}
	return false
}

func (n *naive) dirtyPages() []int {
	out := make([]int, len(n.order))
	copy(out, n.order)
	return out
}

func (n *naive) deps() map[int][]int {
	deps := make(map[int][]int, len(n.out))
	for a, bs := range n.out {
		list := make([]int, 0, len(bs))
		for b := range bs {
			list = append(list, b)
		}
		sort.Ints(list)
		deps[a] = list
	}
	return deps
}

// rndOp 是一条随机操作。
type rndOp struct {
	kind string
	a, b int
	lsn  int64
}

func (o rndOp) String() string {
	switch o.kind {
	case "Modify":
		return fmt.Sprintf("Modify(%d,%d)", o.a, o.lsn)
	case "SetFlushed":
		return fmt.Sprintf("SetFlushed(%d)", o.lsn)
	case "FlushStart":
		return fmt.Sprintf("FlushStart(%d)", o.a)
	case "FlushDone":
		return fmt.Sprintf("FlushDone(%d)", o.a)
	case "AddDep":
		return fmt.Sprintf("AddDep(%d,%d)", o.a, o.b)
	case "Checkpoint":
		return "Checkpoint()"
	case "Plan":
		return fmt.Sprintf("Plan(%d)", o.lsn)
	}
	return "?"
}

func genSeq(r *rand.Rand, count int) []rndOp {
	ops := make([]rndOp, 0, count)
	var nextLSN, flushed int64
	page := func() int { return r.Intn(10) }
	for len(ops) < count {
		switch r.Intn(10) {
		case 0, 1, 2:
			var lsn int64
			if r.Intn(5) == 0 && nextLSN > 0 {
				lsn = 1 + r.Int63n(nextLSN) // 故意生成可能过期的 LSN
			} else {
				nextLSN += 1 + r.Int63n(3)
				lsn = nextLSN
			}
			ops = append(ops, rndOp{kind: "Modify", a: page(), lsn: lsn})
		case 3:
			if r.Intn(6) == 0 && flushed > 0 {
				ops = append(ops, rndOp{kind: "SetFlushed", lsn: r.Int63n(flushed)})
			} else {
				flushed += r.Int63n(4)
				if flushed > nextLSN {
					flushed = nextLSN
				}
				ops = append(ops, rndOp{kind: "SetFlushed", lsn: flushed})
			}
		case 4, 5:
			ops = append(ops, rndOp{kind: "FlushStart", a: page()})
		case 6:
			ops = append(ops, rndOp{kind: "FlushDone", a: page()})
		case 7:
			ops = append(ops, rndOp{kind: "AddDep", a: page(), b: page()})
		case 8:
			ops = append(ops, rndOp{kind: "Checkpoint"})
		case 9:
			target := int64(1)
			if nextLSN > 0 {
				target = 1 + r.Int63n(nextLSN+2)
			}
			ops = append(ops, rndOp{kind: "Plan", lsn: target})
		}
	}
	return ops
}

func codeOf(err *Error) Code {
	if err == nil {
		return 0
	}
	return err.Code
}

// TestRandomAgainstNaive 用 2000 组随机操作序列对照朴素模拟，
// 并验证相同序列重放得到完全相同的链表、依赖边、Checkpoint 与计划。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	const opsPerSeq = 60
	seen := make(map[Code]int)
	for seed := int64(1); seed <= sequences; seed++ {
		r := rand.New(rand.NewSource(seed))
		d := 1 + r.Intn(6)
		ops := genSeq(r, opsPerSeq)
		m, err := NewManager(d)
		if err != nil {
			t.Fatalf("seed=%d NewManager(%d): %v", seed, d, err)
		}
		replay, _ := NewManager(d)
		n := newNaive(d)
		t.Logf("seed=%d D=%d 输入序列共 %d 步", seed, d, len(ops))
		for i, op := range ops {
			desc := op.String()
			var gotCode, wantCode Code
			switch op.kind {
			case "Modify":
				gotCode = codeOf(m.Modify(op.a, op.lsn))
				wantCode = n.Modify(op.a, op.lsn)
				replay.Modify(op.a, op.lsn)
			case "SetFlushed":
				gotCode = codeOf(m.SetFlushed(op.lsn))
				wantCode = n.SetFlushed(op.lsn)
				replay.SetFlushed(op.lsn)
			case "FlushStart":
				gotCode = codeOf(m.FlushStart(op.a))
				wantCode = n.FlushStart(op.a)
				replay.FlushStart(op.a)
			case "FlushDone":
				gotCode = codeOf(m.FlushDone(op.a))
				wantCode = n.FlushDone(op.a)
				replay.FlushDone(op.a)
			case "AddDep":
				gotCode = codeOf(m.AddDep(op.a, op.b))
				wantCode = n.AddDep(op.a, op.b)
				replay.AddDep(op.a, op.b)
			case "Checkpoint":
				if got, want := m.Checkpoint(), n.Checkpoint(); got != want {
					t.Fatalf("seed=%d op[%d] %s: Checkpoint 输出=%d 朴素模拟=%d (判定依据: 逐步模拟规则)",
						seed, i, desc, got, want)
				}
				replay.Checkpoint()
			case "Plan":
				gotPlan, gerr := m.Plan(op.lsn)
				if gerr != nil {
					t.Fatalf("seed=%d op[%d] %s: 意外拒绝 %v", seed, i, desc, gerr)
				}
				if wantPlan := n.Plan(op.lsn); !reflect.DeepEqual(gotPlan, wantPlan) {
					t.Fatalf("seed=%d op[%d] %s: 计划输出=%v 朴素模拟=%v (判定依据: 逐步模拟规则)",
						seed, i, desc, gotPlan, wantPlan)
				}
				replay.Plan(op.lsn)
			}
			if gotCode != wantCode {
				t.Fatalf("seed=%d op[%d] %s: 拒绝码=%v 朴素模拟=%v (判定依据: 逐步模拟规则)",
					seed, i, desc, gotCode, wantCode)
			}
			seen[gotCode]++
			if got, want := m.Checkpoint(), n.Checkpoint(); got != want {
				t.Fatalf("seed=%d op[%d] %s 后: Checkpoint=%d 朴素模拟=%d", seed, i, desc, got, want)
			}
			if got, want := m.DirtyPages(), n.dirtyPages(); !reflect.DeepEqual(got, want) {
				t.Fatalf("seed=%d op[%d] %s 后: 链表=%v 朴素模拟=%v", seed, i, desc, got, want)
			}
			if got, want := m.Dependencies(), n.deps(); !reflect.DeepEqual(got, want) {
				t.Fatalf("seed=%d op[%d] %s 后: 依赖边=%v 朴素模拟=%v", seed, i, desc, got, want)
			}
			t.Logf("seed=%d op[%d] %s => code=%v checkpoint=%d dirty=%v deps=%v 判定: 与朴素模拟一致",
				seed, i, desc, gotCode, m.Checkpoint(), m.DirtyPages(), m.Dependencies())
		}
		if got, want := m.DirtyPages(), replay.DirtyPages(); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%d 重放链表不一致: %v vs %v", seed, got, want)
		}
		if got, want := m.Dependencies(), replay.Dependencies(); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%d 重放依赖边不一致: %v vs %v", seed, got, want)
		}
		if got, want := m.Checkpoint(), replay.Checkpoint(); got != want {
			t.Fatalf("seed=%d 重放 Checkpoint 不一致: %d vs %d", seed, got, want)
		}
		finalTarget := n.maxLSN + 1
		gotPlan, _ := m.Plan(finalTarget)
		wantPlan, _ := replay.Plan(finalTarget)
		if !reflect.DeepEqual(gotPlan, wantPlan) {
			t.Fatalf("seed=%d 重放计划不一致: %v vs %v", seed, gotPlan, wantPlan)
		}
		t.Logf("seed=%d 结束: checkpoint=%d dirty=%v deps=%v plan(%d)=%v 判定: 全部一致",
			seed, m.Checkpoint(), m.DirtyPages(), m.Dependencies(), finalTarget, gotPlan)
	}
	t.Logf("拒绝码覆盖统计: %v", seen)
	for _, c := range []Code{
		CodeLSNNotAdvanced, CodeDirtyPoolFull, CodePageNotDirty, CodePageInFlight,
		CodeLogNotFlushed, CodePredecessorDirty, CodePageNotInFlight, CodeDependencyCycle,
	} {
		if seen[c] == 0 {
			t.Fatalf("随机序列未覆盖拒绝码 %v", c)
		}
	}
}
