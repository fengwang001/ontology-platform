package budget

import (
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// 朴素模拟（仅用于对拍，要求 Π<=4）：对窗口 [s,s+t) 经过的每个对齐周期，
// 独立穷举该周期内 Θ 个时隙的所有放置取最小供给（对抗者可逐周期换位），
// 再穷举窗口起点 s∈[0,Π) 取最小值，得到精确的 sbf。
func naiveSBF(pi, theta, t int64) int64 {
	if theta == pi {
		return t
	}
	if t <= 0 {
		return 0
	}
	best := int64(-1)
	for s := int64(0); s < pi; s++ {
		var supply int64
		// 窗口 [s, s+t) 覆盖时隙 s..s+t-1，逐对齐周期统计。
		for k := s / pi; k*pi <= s+t-1; k++ {
			start := s
			if k*pi > start {
				start = k * pi
			}
			end := s + t
			if (k+1)*pi < end {
				end = (k + 1) * pi
			}
			supply += minPlacementSupply(pi, theta, end-start)
		}
		if best < 0 || supply < best {
			best = supply
		}
	}
	return best
}

// minPlacementSupply 穷举周期内 Θ 个时隙的全部放置，返回与长度 overlap
// 的窗口（从周期起点对齐处切入）的最小交集大小。
func minPlacementSupply(pi, theta, overlap int64) int64 {
	best := int64(-1)
	slots := make([]int64, 0, theta)
	var enumerate func(next int64)
	enumerate = func(next int64) {
		if int64(len(slots)) == theta {
			var supply int64
			for _, sl := range slots {
				if sl < overlap {
					supply++
				}
			}
			if best < 0 || supply < best {
				best = supply
			}
			return
		}
		for u := next; u < pi; u++ {
			slots = append(slots, u)
			enumerate(u + 1)
			slots = slots[:len(slots)-1]
		}
	}
	enumerate(0)
	return best
}

// naiveHorizon 用 big.Int 独立计算 Dmax+H，超过 MaxHorizon 即停止。
func naiveHorizon(pi int64, tasks []Task) (int64, bool) {
	h := big.NewInt(pi)
	var dmax int64
	for _, task := range tasks {
		if task.D > dmax {
			dmax = task.D
		}
		g := new(big.Int).GCD(nil, nil, h, big.NewInt(task.T))
		h.Quo(h, g)
		h.Mul(h, big.NewInt(task.T))
		if h.Cmp(big.NewInt(MaxHorizon)) > 0 {
			return 0, false
		}
	}
	bound := dmax + h.Int64()
	if bound > MaxHorizon {
		return 0, false
	}
	return bound, true
}

// naiveFeasible 逐个整数 t∈[1,bound] 检查 dbf(t)<=naiveSBF(t)。
func naiveFeasible(pi, theta int64, tasks []Task, bound int64) (bool, int64) {
	u := new(big.Rat)
	for _, task := range tasks {
		u.Add(u, big.NewRat(task.C, task.T))
	}
	if u.Cmp(big.NewRat(theta, pi)) > 0 {
		return false, 0
	}
	for tm := int64(1); tm <= bound; tm++ {
		if dbf(tasks, tm) > naiveSBF(pi, theta, tm) {
			return false, tm
		}
	}
	return true, 0
}

// naiveMinBudget 对 Θ=1..Π 线性扫描，返回第一个可行预算。
func naiveMinBudget(pi int64, tasks []Task) (int64, bool, int64) {
	if len(tasks) == 0 {
		return 0, true, 0
	}
	bound, ok := naiveHorizon(pi, tasks)
	if !ok {
		return pi, false, 0
	}
	if ok, vt := naiveFeasible(pi, pi, tasks, bound); !ok {
		return pi, false, vt
	}
	for theta := int64(1); theta <= pi; theta++ {
		if ok, _ := naiveFeasible(pi, theta, tasks, bound); ok {
			return theta, true, 0
		}
	}
	return pi, false, 0
}

// naiveManager 逐步镜像 Manager 的拒绝顺序与状态机，但 MinBudget 用朴素模拟。
type naiveComponent struct {
	pi    int64
	theta int64
	tasks map[string]Task
}

type naiveManager struct {
	components map[string]*naiveComponent
}

func newNaiveManager() *naiveManager {
	return &naiveManager{components: make(map[string]*naiveComponent)}
}

func (n *naiveManager) sortedTasks(c *naiveComponent) []Task {
	tasks := make([]Task, 0, len(c.tasks))
	for _, task := range c.tasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks
}

func (n *naiveManager) declare(name string, pi int64) *RejectError {
	if name == "" || pi < 1 || pi > MaxPeriod {
		return &RejectError{Reason: RejectInvalidParam}
	}
	if _, ok := n.components[name]; ok {
		return &RejectError{Reason: RejectDuplicate}
	}
	if len(n.components) >= MaxComponents {
		return &RejectError{Reason: RejectCapacity}
	}
	n.components[name] = &naiveComponent{pi: pi, tasks: make(map[string]Task)}
	return nil
}

func (n *naiveManager) totalWith(name string, theta int64) bool {
	sum := new(big.Rat)
	for cname, c := range n.components {
		th := c.theta
		if cname == name {
			th = theta
		}
		sum.Add(sum, big.NewRat(th, c.pi))
	}
	return sum.Cmp(big.NewRat(1, 1)) <= 0
}

func (n *naiveManager) addTask(name string, task Task) *RejectError {
	if name == "" || !task.valid() {
		return &RejectError{Reason: RejectInvalidParam}
	}
	c, ok := n.components[name]
	if !ok {
		return &RejectError{Reason: RejectNotFound}
	}
	if _, dup := c.tasks[task.ID]; dup {
		return &RejectError{Reason: RejectDuplicate}
	}
	if len(c.tasks) >= MaxTasksPerComponent {
		return &RejectError{Reason: RejectCapacity}
	}
	candidate := append(n.sortedTasks(c), task)
	if _, ok := naiveHorizon(c.pi, candidate); !ok {
		return &RejectError{Reason: RejectTooLarge}
	}
	minTheta, feasOK, vt := naiveMinBudget(c.pi, candidate)
	if !feasOK {
		return &RejectError{Reason: RejectInfeasible, Theta: c.pi, ViolationT: vt}
	}
	newTheta := c.theta
	if minTheta > newTheta {
		newTheta = minTheta
	}
	if !n.totalWith(name, newTheta) {
		return &RejectError{Reason: RejectOverload, Theta: newTheta}
	}
	c.tasks[task.ID] = task
	c.theta = newTheta
	return nil
}

func (n *naiveManager) removeTask(name, taskID string) *RejectError {
	if name == "" || taskID == "" {
		return &RejectError{Reason: RejectInvalidParam}
	}
	c, ok := n.components[name]
	if !ok {
		return &RejectError{Reason: RejectNotFound}
	}
	if _, ok := c.tasks[taskID]; !ok {
		return &RejectError{Reason: RejectNotFound}
	}
	delete(c.tasks, taskID)
	return nil
}

func (n *naiveManager) compact(name string) *RejectError {
	if name == "" {
		return &RejectError{Reason: RejectInvalidParam}
	}
	c, ok := n.components[name]
	if !ok {
		return &RejectError{Reason: RejectNotFound}
	}
	theta, _, _ := naiveMinBudget(c.pi, n.sortedTasks(c))
	c.theta = theta
	return nil
}

// 闭式 sbf 与时隙穷举朴素模拟逐点一致（Π≤4）。
func TestSBFMatchesNaiveEnumeration(t *testing.T) {
	for pi := int64(1); pi <= 4; pi++ {
		for theta := int64(1); theta <= pi; theta++ {
			for tm := int64(0); tm <= 40; tm++ {
				got := sbf(pi, theta, tm)
				want := naiveSBF(pi, theta, tm)
				if got != want {
					t.Fatalf("sbf(%d,%d,%d)=%d, naive=%d", pi, theta, tm, got, want)
				}
			}
		}
	}
}

type fuzzOp struct {
	kind   string
	name   string
	pi     int64
	task   Task
	taskID string
}

func genOp(rng *rand.Rand, names []string) fuzzOp {
	name := names[rng.Intn(len(names))]
	switch rng.Intn(10) {
	case 0, 1: // Declare
		return fuzzOp{kind: "declare", name: name, pi: int64(1 + rng.Intn(4))}
	case 2: // RemoveTask
		return fuzzOp{kind: "remove", name: name, taskID: fmt.Sprintf("t%d", rng.Intn(6))}
	case 3: // Compact
		return fuzzOp{kind: "compact", name: name}
	case 4: // 非法参数
		return fuzzOp{kind: "declare", name: "", pi: 0}
	default: // AddTask
		tm := int64(1 + rng.Intn(6))
		d := int64(1 + rng.Intn(int(tm)))
		c := int64(1 + rng.Intn(int(d)))
		return fuzzOp{
			kind: "add",
			name: name,
			task: Task{ID: fmt.Sprintf("t%d", rng.Intn(6)), C: c, T: tm, D: d},
		}
	}
}

func applyReal(m *Manager, op fuzzOp) *RejectError {
	var err error
	switch op.kind {
	case "declare":
		err = m.Declare(op.name, op.pi)
	case "add":
		err = m.AddTask(op.name, op.task)
	case "remove":
		err = m.RemoveTask(op.name, op.taskID)
	case "compact":
		err = m.Compact(op.name)
	}
	if err == nil {
		return nil
	}
	return err.(*RejectError)
}

func applyNaive(n *naiveManager, op fuzzOp) *RejectError {
	switch op.kind {
	case "declare":
		return n.declare(op.name, op.pi)
	case "add":
		return n.addTask(op.name, op.task)
	case "remove":
		return n.removeTask(op.name, op.taskID)
	case "compact":
		return n.compact(op.name)
	}
	return nil
}

func reasonOf(err *RejectError) string {
	if err == nil {
		return "ok"
	}
	return fmt.Sprintf("%v(theta=%d,vt=%d)", err.Reason, err.Theta, err.ViolationT)
}

// 对拍：2000 组随机操作序列，真实实现（闭式 sbf + 二分 + 跳变点检查）
// 与朴素模拟（时隙穷举 sbf + 线性扫描 Θ + 逐整数 t 检查）逐步比对。
func TestDifferentialRandomSequences(t *testing.T) {
	names := []string{"A", "B", "C", "D", "E"}
	for seq := 0; seq < 2000; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		real := NewManager()
		naive := newNaiveManager()
		ops := 10 + rng.Intn(20)
		for step := 0; step < ops; step++ {
			op := genOp(rng, names)
			realErr := applyReal(real, op)
			naiveErr := applyNaive(naive, op)
			if reasonOf(realErr) != reasonOf(naiveErr) {
				t.Fatalf("seq=%d step=%d op=%+v: real=%s naive=%s",
					seq, step, op, reasonOf(realErr), reasonOf(naiveErr))
			}
			// 比对所有组件 θ 与 Total。
			allNames := map[string]bool{}
			for cname := range real.components {
				allNames[cname] = true
			}
			for cname := range naive.components {
				allNames[cname] = true
			}
			for cname := range allNames {
				rb, rerr := real.Budget(cname)
				nc, nok := naive.components[cname]
				if (rerr != nil) != !nok {
					t.Fatalf("seq=%d step=%d: component %q presence mismatch", seq, step, cname)
				}
				if rerr == nil && rb != nc.theta {
					t.Fatalf("seq=%d step=%d op=%+v: theta[%q] real=%d naive=%d",
						seq, step, op, cname, rb, nc.theta)
				}
			}
			naiveTotal := new(big.Rat)
			for _, c := range naive.components {
				naiveTotal.Add(naiveTotal, big.NewRat(c.theta, c.pi))
			}
			if real.Total().Cmp(naiveTotal) != 0 {
				t.Fatalf("seq=%d step=%d: total real=%v naive=%v", seq, step, real.Total(), naiveTotal)
			}
			t.Logf("seq=%d step=%d op=%+v -> %s total=%v", seq, step, op, reasonOf(realErr), real.Total())
			checkInvariants(t, real)
		}
	}
}

// checkInvariants 验证不变量：Σθ/Π≤1、θ≥MinBudget(当前任务集)、任务集在 θ 下可调度。
func checkInvariants(t *testing.T, m *Manager) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	sum := new(big.Rat)
	for _, c := range m.components {
		sum.Add(sum, big.NewRat(c.theta, c.pi))
		tasks := c.taskSlice()
		if len(tasks) == 0 {
			continue
		}
		dmax, h, ok := horizon(c.pi, tasks)
		if !ok {
			t.Fatalf("component %q: horizon exceeded after accepted ops", c.name)
		}
		minTheta, feasOK, _ := minBudget(c.pi, tasks, dmax, h)
		if !feasOK {
			t.Fatalf("component %q: accepted task set infeasible at theta=Pi", c.name)
		}
		if c.theta < minTheta {
			t.Fatalf("component %q: theta=%d < MinBudget=%d", c.name, c.theta, minTheta)
		}
		if ok, vt := feasible(c.pi, c.theta, tasks, dmax, h); !ok {
			t.Fatalf("component %q: not schedulable at theta=%d (t=%d)", c.name, c.theta, vt)
		}
	}
	if sum.Cmp(big.NewRat(1, 1)) > 0 {
		t.Fatalf("global bandwidth %v > 1", sum)
	}
}

// 相同操作序列重放得到完全相同的 θ 与拒绝点。
func TestDeterministicReplay(t *testing.T) {
	names := []string{"A", "B", "C"}
	rng := rand.New(rand.NewSource(1234))
	ops := make([]fuzzOp, 200)
	for i := range ops {
		ops[i] = genOp(rng, names)
	}
	run := func() []string {
		m := NewManager()
		trace := make([]string, 0, len(ops))
		for _, op := range ops {
			err := applyReal(m, op)
			trace = append(trace, fmt.Sprintf("%s|%v", reasonOf(err), m.Total()))
		}
		return trace
	}
	first := run()
	second := run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay diverged at op %d (%+v): %s vs %s", i, ops[i], first[i], second[i])
		}
	}
}

// 并发调用：结果等价于某个串行顺序，且任意时刻不变量成立（配合 -race）。
func TestConcurrentOps(t *testing.T) {
	m := NewManager()
	names := []string{"A", "B", "C", "D"}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 200; i++ {
				op := genOp(rng, names)
				applyReal(m, op)
				_, _ = m.Budget(names[rng.Intn(len(names))])
				_ = m.Total()
			}
		}(int64(g * 977))
	}
	wg.Wait()
	checkInvariants(t, m)
}
