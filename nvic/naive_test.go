package nvic

import (
	"fmt"
	"math/rand"
	"testing"
)

// 本文件包含一个独立的「逐步朴素模拟」参考模型：
// 它严格按题目规则逐字段维护状态、逐步给出判定依据，
// 与生产实现 nvic.go 完全独立，用于随机序列对照。

type opKind int

const (
	opEnable opKind = iota
	opDisable
	opPend
	opClear
	opSetPriority
	opSetBase
	opReturn
)

// Op 是一次操作的与模型无关的表示。
type Op struct {
	Kind opKind
	IRQ  int
	Arg  int
}

func (o Op) String() string {
	switch o.Kind {
	case opEnable:
		return fmt.Sprintf("Enable(%d)", o.IRQ)
	case opDisable:
		return fmt.Sprintf("Disable(%d)", o.IRQ)
	case opPend:
		return fmt.Sprintf("Pend(%d)", o.IRQ)
	case opClear:
		return fmt.Sprintf("Clear(%d)", o.IRQ)
	case opSetPriority:
		return fmt.Sprintf("SetPriority(%d,%d)", o.IRQ, o.Arg)
	case opSetBase:
		return fmt.Sprintf("SetBase(%d)", o.Arg)
	default:
		return "Return()"
	}
}

// naiveModel 是朴素参考实现。
type naiveModel struct {
	m, s              int
	enable, pend, act []bool
	prio              []int
	base              int
	stack             []int
}

func newNaive(m, s int) *naiveModel {
	return &naiveModel{
		m:      m,
		s:      s,
		enable: make([]bool, m),
		pend:   make([]bool, m),
		act:    make([]bool, m),
		prio:   make([]int, m),
	}
}

func (nm *naiveModel) group(irq int) int { return nm.prio[irq] >> nm.s }
func (nm *naiveModel) sub(irq int) int   { return nm.prio[irq] & (1<<nm.s - 1) }

func (nm *naiveModel) masked(g int) bool {
	return nm.base != 0 && g >= nm.base>>nm.s
}

// candidate 逐步扫描，选（组，子，编号）最小者。
func (nm *naiveModel) candidate() (int, bool) {
	best := -1
	for i := 0; i < nm.m; i++ {
		if !nm.enable[i] || !nm.pend[i] || nm.act[i] {
			continue
		}
		if nm.masked(nm.group(i)) {
			continue
		}
		if best == -1 {
			best = i
			continue
		}
		gi, gb := nm.group(i), nm.group(best)
		if gi < gb ||
			(gi == gb && (nm.sub(i) < nm.sub(best) ||
				(nm.sub(i) == nm.sub(best) && i < best))) {
			best = i
		}
	}
	return best, best >= 0
}

func (nm *naiveModel) runningLevel() (int, bool) {
	if len(nm.stack) == 0 {
		return 0, false
	}
	return nm.group(nm.stack[len(nm.stack)-1]), true
}

// step 施加一次操作，返回（事件, 拒绝原因索引）。
// reject 为 -1 表示成功；否则索引与 rejectErr 表一一对应。
func (nm *naiveModel) step(o Op) ([]Event, int) {
	badIRQ := o.IRQ < 0 || o.IRQ >= nm.m

	switch o.Kind {
	case opEnable, opDisable, opPend, opClear:
		if badIRQ {
			return nil, 3 // 编号越界
		}
		switch o.Kind {
		case opEnable:
			nm.enable[o.IRQ] = true
		case opDisable:
			nm.enable[o.IRQ] = false
		case opPend:
			nm.pend[o.IRQ] = true
		case opClear:
			nm.pend[o.IRQ] = false
		}
		return nm.dispatch(), -1

	case opSetPriority:
		// 编号越界优先于优先级非法。
		if badIRQ {
			return nil, 3
		}
		if o.Arg < 0 || o.Arg > 255 {
			return nil, 4
		}
		nm.prio[o.IRQ] = o.Arg
		return nm.dispatch(), -1

	case opSetBase:
		if o.Arg < 0 || o.Arg > 255 {
			return nil, 5
		}
		nm.base = o.Arg
		return nm.dispatch(), -1

	default: // opReturn
		if len(nm.stack) == 0 {
			return nil, 6
		}
		top := nm.stack[len(nm.stack)-1]
		nm.stack = nm.stack[:len(nm.stack)-1]
		nm.act[top] = false
		evs := []Event{{Kind: Exit, IRQ: top}}
		cand, ok := nm.candidate()
		level, running := nm.runningLevel()
		if ok && (!running || nm.group(cand) < level) {
			nm.pend[cand] = false
			nm.act[cand] = true
			nm.stack = append(nm.stack, cand)
			evs = append(evs, Event{Kind: TailChain, IRQ: cand})
			return evs, -1
		}
		if running {
			evs = append(evs, Event{Kind: Resume, IRQ: nm.stack[len(nm.stack)-1]})
		} else {
			evs = append(evs, Event{Kind: Idle, IRQ: -1})
		}
		return evs, -1
	}
}

// dispatch 对应非 Return 操作施加后的一次调度。
func (nm *naiveModel) dispatch() []Event {
	cand, ok := nm.candidate()
	if !ok {
		return []Event{}
	}
	level, running := nm.runningLevel()
	if running && nm.group(cand) >= level {
		// 组优先级不严格小于运行级：相同组仅子优先级更小也不抢占。
		return []Event{}
	}
	nm.pend[cand] = false
	nm.act[cand] = true
	kind := Enter
	if running {
		kind = Preempt
	}
	nm.stack = append(nm.stack, cand)
	return []Event{{Kind: kind, IRQ: cand}}
}

var rejectErr = map[int]error{
	3: ErrIRQOutOfRange,
	4: ErrInvalidPriority,
	5: ErrInvalidBase,
	6: ErrReturnFromThread,
}

// applyOnReal 在生产实现上施加同一操作。
func applyOnReal(n *NVIC, o Op) ([]Event, error) {
	switch o.Kind {
	case opEnable:
		return n.Enable(o.IRQ)
	case opDisable:
		return n.Disable(o.IRQ)
	case opPend:
		return n.Pend(o.IRQ)
	case opClear:
		return n.Clear(o.IRQ)
	case opSetPriority:
		return n.SetPriority(o.IRQ, o.Arg)
	case opSetBase:
		return n.SetBase(o.Arg)
	default:
		return n.Return()
	}
}

// explainEvents 给出朴素模型对每个事件的判定依据，供日志复现。
func explainEvents(evs []Event) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		switch e.Kind {
		case Enter:
			out = append(out, "thread mode: stack empty, candidate enters")
		case Preempt:
			out = append(out, "candidate group strictly smaller than running level")
		case Exit:
			out = append(out, "top of stack returns, active cleared")
		case TailChain:
			out = append(out, "candidate group strictly smaller than new top; bypass thread mode")
		case Resume:
			out = append(out, "no eligible tail-chain; resume preempted handler on stack")
		case Idle:
			out = append(out, "stack empty and no eligible candidate")
		}
	}
	return out
}

// runScenario 在两种实现上重放同一序列，逐步校验事件、错误、标志与栈；
// verbose 时打印输入、输出与判定依据。
func runScenario(t *testing.T, m, s int, ops []Op, verbose bool) (*NVIC, *naiveModel) {
	t.Helper()
	n, err := New(m, s)
	if err != nil {
		t.Fatalf("New(%d,%d) unexpected error: %v", m, s, err)
	}
	nm := newNaive(m, s)
	if verbose {
		t.Logf("=== New(M=%d, s=%d): group=p>>%d, sub=p&0x%x; flags all false; base=0 ===",
			m, s, s, 1<<s-1)
	}
	for i, o := range ops {
		realEvs, realErr := applyOnReal(n, o)
		naiveEvs, rej := nm.step(o)
		if verbose {
			t.Logf("step %d input : %s", i, o)
		}
		if rej != -1 {
			if realErr != rejectErr[rej] {
				t.Fatalf("step %d %s: real err=%v want %v", i, o, realErr, rejectErr[rej])
			}
			if verbose {
				t.Logf("          reject: %v (state unchanged)", realErr)
			}
			assertStateEqual(t, n, nm, i)
			continue
		}
		if realErr != nil {
			t.Fatalf("step %d %s: unexpected real error %v", i, o, realErr)
		}
		if !eventsEqual(realEvs, naiveEvs) {
			t.Fatalf("step %d %s: events %v want %v", i, o, realEvs, naiveEvs)
		}
		if verbose {
			for j, why := range explainEvents(naiveEvs) {
				t.Logf("          output: %-14s [%s]", naiveEvs[j], why)
			}
			if len(naiveEvs) == 0 {
				t.Logf("          output: <no events>  [no candidate or group not strictly smaller]")
			}
		}
		assertStateEqual(t, n, nm, i)
		assertInvariant(t, n, nm, i)
	}
	return n, nm
}

func eventsEqual(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertStateEqual(t *testing.T, n *NVIC, nm *naiveModel, step int) {
	t.Helper()
	for i := 0; i < nm.m; i++ {
		if en, _ := n.Enabled(i); en != nm.enable[i] {
			t.Fatalf("step %d: enable[%d] %v want %v", step, i, en, nm.enable[i])
		}
		if p, _ := n.Pending(i); p != nm.pend[i] {
			t.Fatalf("step %d: pend[%d] %v want %v", step, i, p, nm.pend[i])
		}
		if a, _ := n.Active(i); a != nm.act[i] {
			t.Fatalf("step %d: active[%d] %v want %v", step, i, a, nm.act[i])
		}
		if p, _ := n.Priority(i); p != nm.prio[i] {
			t.Fatalf("step %d: prio[%d] %d want %d", step, i, p, nm.prio[i])
		}
	}
	if n.Base() != nm.base {
		t.Fatalf("step %d: base %d want %d", step, n.Base(), nm.base)
	}
	rs := n.Stack()
	if len(rs) != len(nm.stack) {
		t.Fatalf("step %d: stack %v want %v", step, rs, nm.stack)
	}
	for i := range rs {
		if rs[i] != nm.stack[i] {
			t.Fatalf("step %d: stack %v want %v", step, rs, nm.stack)
		}
	}
}

// assertInvariant 校验结构不变量：
//  1. 活动标志为真的中断恰好是栈内中断，且栈内无重复；
//  2. 操作完成后不存在组优先级严格小于当前运行级的候选。
func assertInvariant(t *testing.T, n *NVIC, nm *naiveModel, step int) {
	t.Helper()
	seen := map[int]bool{}
	rs := n.Stack()
	for _, irq := range rs {
		if seen[irq] {
			t.Fatalf("step %d: irq %d duplicated in stack %v", step, irq, rs)
		}
		seen[irq] = true
		if a, _ := n.Active(irq); !a {
			t.Fatalf("step %d: stack irq %d not active", step, irq)
		}
	}
	for i := 0; i < nm.m; i++ {
		if a, _ := n.Active(i); a && !seen[i] {
			t.Fatalf("step %d: irq %d active but not on stack %v", step, i, rs)
		}
	}
	cand, ok := nm.candidate()
	level, running := nm.runningLevel()
	if ok && running && nm.group(cand) < level {
		t.Fatalf("step %d: candidate %d group %d < running level %d after settle",
			step, cand, nm.group(cand), level)
	}
}

// TestNaiveRandomCompare 随机生成操作序列（含非法参数），双实现逐步对照。
func TestNaiveRandomCompare(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	const trials = 400
	for trial := 0; trial < trials; trial++ {
		m := 1 + rng.Intn(6)
		s := rng.Intn(8)
		nm := newNaive(m, s)
		n, err := New(m, s)
		if err != nil {
			t.Fatalf("trial %d New: %v", trial, err)
		}
		for k := 0; k < 60; k++ {
			o := randomOp(rng, m)
			realEvs, realErr := applyOnReal(n, o)
			naiveEvs, rej := nm.step(o)
			if rej != -1 {
				if realErr != rejectErr[rej] {
					t.Fatalf("trial %d step %d %s: err %v want %v",
						trial, k, o, realErr, rejectErr[rej])
				}
			} else if realErr != nil || !eventsEqual(realEvs, naiveEvs) {
				t.Fatalf("trial %d (m=%d,s=%d) step %d %s: real=(%v,%v) naive=%v",
					trial, m, s, k, o, realEvs, realErr, naiveEvs)
			}
			assertStateEqual(t, n, nm, k)
			assertInvariant(t, n, nm, k)
		}
	}
}

func randomOp(rng *rand.Rand, m int) Op {
	// 偶尔生成越界编号或非法优先级/阈值，验证拒绝路径。
	irq := rng.Intn(m + 2)
	if rng.Intn(8) == 0 {
		irq = m + rng.Intn(2) // 越界
	}
	switch rng.Intn(7) {
	case 0:
		return Op{Kind: opEnable, IRQ: irq}
	case 1:
		return Op{Kind: opDisable, IRQ: irq}
	case 2:
		return Op{Kind: opPend, IRQ: irq}
	case 3:
		return Op{Kind: opClear, IRQ: irq}
	case 4:
		p := rng.Intn(280) // 0..255 合法，256..279 非法
		return Op{Kind: opSetPriority, IRQ: irq, Arg: p}
	case 5:
		return Op{Kind: opSetBase, Arg: rng.Intn(280)}
	default:
		return Op{Kind: opReturn}
	}
}
