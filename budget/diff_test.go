package budget

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

// refModel 是按规则手写的逐步朴素参考实现：
// 每步重算全部受影响状态，MinBudget 用 naiveMinBudget（整数 t 扫描 + 穷举 sbf）。
type refModel struct {
	t     *testing.T
	comps map[string]map[string]Task
	pi    map[string]int64
	theta map[string]int64
}

func newRef(t *testing.T) *refModel {
	return &refModel{
		t:     t,
		comps: map[string]map[string]Task{},
		pi:    map[string]int64{},
		theta: map[string]int64{},
	}
}

func (r *refModel) taskList(name string) []Task {
	ids := make([]string, 0)
	for id := range r.comps[name] {
		ids = append(ids, id)
	}
	// 与真实实现不同：故意按字典序排列，验证 MinBudget 与输入顺序无关。
	sortStrings(ids)
	list := make([]Task, 0, len(ids))
	for _, id := range ids {
		list = append(list, r.comps[name][id])
	}
	return list
}

func (r *refModel) declare(name string, pi int64) string {
	if name == "" || !validatePi(pi) {
		return string(ReasonInvalid)
	}
	if _, ok := r.comps[name]; ok {
		return string(ReasonDuplicate)
	}
	if len(r.comps) >= 8 {
		return string(ReasonCapacity)
	}
	r.comps[name] = map[string]Task{}
	r.pi[name] = pi
	r.theta[name] = 0
	return ""
}

func (r *refModel) minBudgetTasks(name string, tasks []Task) (int64, string, int64) {
	if len(tasks) == 0 {
		return 0, "", 0
	}
	_, _, ok := checkHorizon(r.pi[name], tasks)
	if !ok {
		return 0, string(ReasonTooLarge), 0
	}
	th, feas, bad := naiveMinBudget(r.t, r.pi[name], tasks)
	if !feas {
		return th, string(ReasonInfeasible), bad
	}
	return th, "", 0
}

func (r *refModel) totalWith(name string, thetaNext int64) *big.Rat {
	sum := new(big.Rat)
	for cn := range r.comps {
		th := r.theta[cn]
		if cn == name {
			th = thetaNext
		}
		sum.Add(sum, bigRat(th, r.pi[cn]))
	}
	return sum
}

func (r *refModel) addTask(name string, tk Task) string {
	if name == "" || !validateTask(tk) {
		return string(ReasonInvalid)
	}
	c, ok := r.comps[name]
	if !ok {
		return string(ReasonNotFound)
	}
	if _, dup := c[tk.ID]; dup {
		return string(ReasonDuplicate)
	}
	if len(c) >= 8 {
		return string(ReasonCapacity)
	}
	tasks := r.taskList(name)
	tasks = append(tasks, tk)
	mb, why, _ := r.minBudgetTasks(name, tasks)
	if why != "" {
		return why
	}
	newTheta := r.theta[name]
	if mb > newTheta {
		newTheta = mb
	}
	if r.totalWith(name, newTheta).Cmp(bigRat1()) > 0 {
		return string(ReasonOverloaded)
	}
	c[tk.ID] = tk
	r.theta[name] = newTheta
	return ""
}

func (r *refModel) removeTask(name, id string) string {
	if name == "" || id == "" {
		return string(ReasonInvalid)
	}
	c, ok := r.comps[name]
	if !ok {
		return string(ReasonNotFound)
	}
	if _, has := c[id]; !has {
		return string(ReasonNotFound)
	}
	delete(c, id)
	return ""
}

func (r *refModel) compact(name string) string {
	if name == "" {
		return string(ReasonInvalid)
	}
	if _, ok := r.comps[name]; !ok {
		return string(ReasonNotFound)
	}
	tasks := r.taskList(name)
	if len(tasks) == 0 {
		r.theta[name] = 0
		return ""
	}
	mb, why, _ := r.minBudgetTasks(name, tasks)
	if why != "" {
		return why
	}
	r.theta[name] = mb
	return ""
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// op 是一条随机操作。
type op struct {
	kind string
	name string
	pi   int64
	task Task
}

func TestRandomDifferential2000(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	rng := rand.New(rand.NewSource(424242))
	for seq := 0; seq < 2000; seq++ {
		runOneSequence(t, rng, seq)
	}
}

func runOneSequence(t *testing.T, rng *rand.Rand, seq int) {
	m := NewManager()
	ref := newRef(t)
	names := []string{}
	taskIDs := map[string][]string{}
	logs := []string{fmt.Sprintf("seq=%d", seq)}

	steps := 3 + rng.Intn(10)
	for step := 0; step < steps; step++ {
		var o op
		switch rng.Intn(5) {
		case 0: // Declare
			name := fmt.Sprintf("c%d", rng.Intn(6)) // 允许偶发重名
			pi := int64(1 + rng.Intn(4))            // Pi <= 4 才能走穷举对拍
			o = op{kind: "declare", name: name, pi: pi}
		case 1: // AddTask
			if len(names) == 0 {
				step--
				continue
			}
			name := names[rng.Intn(len(names))]
			id := fmt.Sprintf("t%d", rng.Intn(10))
			period := int64(1 + rng.Intn(6))
			c := int64(1 + rng.Intn(int(period)))
			d := c + int64(rng.Intn(int(period-c+1)))
			o = op{kind: "add", name: name, task: Task{ID: id, C: c, T: period, D: d}}
		case 2: // RemoveTask
			if len(names) == 0 || len(taskIDs[names[0]]) == 0 {
				step--
				continue
			}
			name := names[rng.Intn(len(names))]
			if len(taskIDs[name]) == 0 {
				step--
				continue
			}
			id := taskIDs[name][rng.Intn(len(taskIDs[name]))]
			o = op{kind: "remove", name: name, task: Task{ID: id}}
		case 3: // Compact
			if len(names) == 0 {
				step--
				continue
			}
			o = op{kind: "compact", name: names[rng.Intn(len(names))]}
		case 4: // 查询
			if len(names) == 0 {
				step--
				continue
			}
			o = op{kind: "budget", name: names[rng.Intn(len(names))]}
		}

		var got, want string
		switch o.kind {
		case "declare":
			logs = append(logs, fmt.Sprintf("step=%d Declare(%q,%d)", step, o.name, o.pi))
			if err := m.Declare(o.name, o.pi); err != nil {
				got = string(reasonOf(err))
			}
			want = ref.declare(o.name, o.pi)
			if got == "" && want == "" {
				names = appendIfMissing(names, o.name)
				taskIDs[o.name] = nil
			}
		case "add":
			logs = append(logs, fmt.Sprintf("step=%d AddTask(%q,%+v)", step, o.name, o.task))
			if err := m.AddTask(o.name, o.task); err != nil {
				got = string(reasonOf(err))
			}
			want = ref.addTask(o.name, o.task)
			if got == "" && want == "" {
				taskIDs[o.name] = appendIfMissing(taskIDs[o.name], o.task.ID)
			}
		case "remove":
			logs = append(logs, fmt.Sprintf("step=%d RemoveTask(%q,%q)", step, o.name, o.task.ID))
			if err := m.RemoveTask(o.name, o.task.ID); err != nil {
				got = string(reasonOf(err))
			}
			want = ref.removeTask(o.name, o.task.ID)
			if got == "" && want == "" {
				taskIDs[o.name] = removeString(taskIDs[o.name], o.task.ID)
			}
		case "compact":
			logs = append(logs, fmt.Sprintf("step=%d Compact(%q)", step, o.name))
			if err := m.Compact(o.name); err != nil {
				got = string(reasonOf(err))
			}
			want = ref.compact(o.name)
		case "budget":
			b, err := m.Budget(o.name)
			if err != nil {
				got = string(reasonOf(err))
			}
			if wantB := ref.theta[o.name]; b != wantB && err == nil {
				t.Fatalf("seq=%d step=%d Budget(%s)=%d ref=%d\n%s", seq, step, o.name, b, wantB, joinLogs(logs))
			}
		}
		logs = append(logs, fmt.Sprintf("  -> got=%q ref=%q", got, want))
		if got != want {
			t.Fatalf("seq=%d step=%d op=%s mismatch\n%s", seq, step, o.kind, joinLogs(logs))
		}
		// 每步对比预算与 Total。
		for _, name := range names {
			b, _ := m.Budget(name)
			if b != ref.theta[name] {
				t.Fatalf("seq=%d step=%d theta(%s)=%d ref=%d\n%s", seq, step, name, b, ref.theta[name], joinLogs(logs))
			}
		}
		if gotT, wantT := m.Total(), ratToFraction(ref.totalWith("", 0)); gotT != wantT {
			t.Fatalf("seq=%d step=%d total=%s ref=%s\n%s", seq, step, gotT, wantT, joinLogs(logs))
		}
	}
	t.Logf("%s", joinLogs(logs))
}

func ratToFraction(r *big.Rat) Fraction {
	if r.Sign() == 0 {
		return Fraction{0, 1}
	}
	return Fraction{r.Num().Int64(), r.Denom().Int64()}
}

func appendIfMissing(xs []string, x string) []string {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	return append(xs, x)
}

func removeString(xs []string, x string) []string {
	for i, v := range xs {
		if v == x {
			return append(xs[:i], xs[i+1:]...)
		}
	}
	return xs
}

func joinLogs(logs []string) string {
	out := ""
	for _, l := range logs {
		out += l + "\n"
	}
	return out
}
