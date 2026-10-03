package rta

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// ---- 朴素参考实现（与生产代码完全独立） ----

type refTask struct {
	id            string
	c, t, d, j, b int64
}

type refState struct {
	task refTask
	resp int64
}

type refScheduler struct {
	tasks map[string]*refState
	order []string // 高 -> 低
}

// naiveResponse 朴素逐步模拟：w 自 C+B 起逐 1 递增，寻找最小满足
// demand(w)<=w 的值；每个出现的 w（含初值）都先做 w+J>D 判定。
func naiveResponse(tk refTask, higher []refTask, limit int64) (int64, bool) {
	w := tk.c + tk.b
	for w <= limit {
		if w+tk.j > tk.d {
			return 0, false
		}
		var demand int64 = tk.c + tk.b
		for _, h := range higher {
			num := w + h.j
			q := num / h.t
			if num%h.t != 0 {
				q++
			}
			add := q * h.c
			if demand > limit-add {
				demand = limit + 1
				break
			}
			demand += add
		}
		if demand > limit {
			return 0, false
		}
		if demand <= w {
			return w + tk.j, true
		}
		w++
	}
	return 0, false
}

func refFeasible(order []refTask, limit int64) bool {
	for i := range order {
		if _, ok := naiveResponse(order[i], order[:i], limit); !ok {
			return false
		}
	}
	return true
}

// refAudsley 朴素 Audsley：最低位优先，编号字节序最小的可调度候选占该位。
func refAudsley(tasks []refTask, limit int64) ([]refTask, int, bool) {
	remaining := append([]refTask(nil), tasks...)
	lowToHigh := make([]refTask, 0, len(remaining))
	for len(remaining) > 0 {
		picked := -1
		for i := range remaining {
			higher := append(append([]refTask(nil), remaining[:i]...), remaining[i+1:]...)
			if _, ok := naiveResponse(remaining[i], higher, limit); ok {
				if picked == -1 || remaining[i].id < remaining[picked].id {
					picked = i
				}
			}
		}
		if picked == -1 {
			return nil, len(remaining), false
		}
		lowToHigh = append(lowToHigh, remaining[picked])
		remaining = append(remaining[:picked], remaining[picked+1:]...)
	}
	for i, j := 0, len(lowToHigh)-1; i < j; i, j = i+1, j-1 {
		lowToHigh[i], lowToHigh[j] = lowToHigh[j], lowToHigh[i]
	}
	return lowToHigh, 0, true
}

func refValid(tk refTask) bool {
	if len(tk.id) == 0 || len(tk.id) > 64 {
		return false
	}
	return 1 <= tk.c && tk.c <= 1e9 &&
		1 <= tk.t && tk.t <= 1e9 &&
		1 <= tk.d && tk.d <= tk.t &&
		0 <= tk.j && tk.j <= 1e9 &&
		0 <= tk.b && tk.b <= 1e9
}

const refLimit int64 = 200_000 // 朴素逐 1 模拟的安全上界（测试参数远小于此）

type refResult struct {
	reordered  bool
	errReason  Reason
	unassigned int
}

func (r *refScheduler) taskList(order []string) []refTask {
	out := make([]refTask, len(order))
	for i, id := range order {
		out[i] = r.tasks[id].task
	}
	return out
}

func (r *refScheduler) commit(order []refTask, from int) {
	r.order = make([]string, len(order))
	for i, x := range order {
		r.order[i] = x.id
		if i >= from {
			rr, _ := naiveResponse(x, order[:i], refLimit)
			r.tasks[x.id].resp = rr
		}
	}
}

func (r *refScheduler) add(tk refTask) refResult {
	if !refValid(tk) {
		return refResult{errReason: ReasonInvalidParam}
	}
	if _, dup := r.tasks[tk.id]; dup {
		return refResult{errReason: ReasonDuplicateID}
	}
	if len(r.order) >= MaxTasks {
		return refResult{errReason: ReasonCapacityFull}
	}
	list := r.taskList(r.order)
	// 插入尝试：自最低位（末尾）向前。
	for p := len(list); p >= 0; p-- {
		cand := make([]refTask, 0, len(list)+1)
		cand = append(cand, list[:p]...)
		cand = append(cand, tk)
		cand = append(cand, list[p:]...)
		// 前缀 [0:p) 恒可调度；从 p 起检查即可，但全量检查更直观。
		if !refFeasible(cand, refLimit) {
			continue
		}
		r.tasks[tk.id] = &refState{task: tk}
		r.commit(cand, 0)
		return refResult{}
	}
	// Audsley 回退。
	all := append(list, tk)
	newOrder, unassigned, ok := refAudsley(all, refLimit)
	if !ok {
		return refResult{errReason: ReasonUnschedulable, unassigned: unassigned}
	}
	r.tasks[tk.id] = &refState{task: tk}
	r.commit(newOrder, 0)
	return refResult{reordered: true}
}

func (r *refScheduler) remove(id string) Reason {
	if len(id) == 0 {
		return ReasonInvalidParam
	}
	if _, ok := r.tasks[id]; !ok {
		return ReasonNotFound
	}
	pos := 0
	for r.order[pos] != id {
		pos++
	}
	r.order = append(r.order[:pos], r.order[pos+1:]...)
	delete(r.tasks, id)
	list := r.taskList(r.order)
	r.commit(list, pos)
	return 0
}

// feasiblePermutation 穷举全排列，判断是否存在可行全序。
func feasiblePermutation(tasks []refTask) bool {
	var found bool
	perm := make([]refTask, 0, len(tasks))
	var gen func(int)
	gen = func(k int) {
		if found {
			return
		}
		if k == len(tasks) {
			if refFeasible(perm, refLimit) {
				found = true
			}
			return
		}
		for i := k; i < len(tasks); i++ {
			tasks[k], tasks[i] = tasks[i], tasks[k]
			perm = append(perm, tasks[k])
			gen(k + 1)
			perm = perm[:k]
			tasks[k], tasks[i] = tasks[i], tasks[k]
		}
	}
	gen(0)
	return found
}

func toRef(t Task) refTask {
	return refTask{id: t.ID, c: t.C, t: t.T, d: t.D, j: t.J, b: t.B}
}

// randomTask 生成任务，偶尔注入非法参数；id 空间 t0..t5（至多 6 个唯一任务，
// 全排列穷举 <=720）。
func randomTask(rng *rand.Rand) (Task, bool) {
	tp := rng.Int63n(12) + 1
	tk := Task{
		ID: fmt.Sprintf("t%d", rng.Intn(6)),
		C:  rng.Int63n(8) + 1,
		T:  tp,
		D:  rng.Int63n(tp) + 1,
		J:  rng.Int63n(6),
		B:  rng.Int63n(4),
	}
	valid := true
	switch rng.Intn(14) {
	case 0:
		tk.C = 0
		valid = false
	case 1:
		tk.D = tp + 1
		valid = false
	case 2:
		tk.J = -1
		valid = false
	case 3:
		tk.ID = ""
		valid = false
	case 4:
		tk.B = 1_000_000_001
		valid = false
	}
	return tk, valid
}

func describeTask(tk Task) string {
	return fmt.Sprintf("%s{C=%d,T=%d,D=%d,J=%d,B=%d}", tk.ID, tk.C, tk.T, tk.D, tk.J, tk.B)
}

func prodReason(err error) (Reason, int) {
	if err == nil {
		return 0, 0
	}
	re := err.(*Error)
	return re.Reason, re.Unassigned
}

func reasonName(r Reason) string {
	switch r {
	case ReasonInvalidParam:
		return "InvalidParam"
	case ReasonDuplicateID:
		return "DuplicateID"
	case ReasonCapacityFull:
		return "CapacityFull"
	case ReasonUnschedulable:
		return "Unschedulable"
	case ReasonNotFound:
		return "NotFound"
	default:
		return "OK"
	}
}

func (s *Scheduler) orderString() []string {
	out := make([]string, len(s.order))
	copy(out, s.order)
	return out
}

// TestRandomDifferential 2000 组随机操作序列，与朴素参考实现逐步对拍；
// 任务集 <=6 时额外穷举全部排列核对可行性，并验证 Audsley 成功序全员可调度。
// 日志打印每组序列的输入、输出与判定依据（-v 可见）。
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		seed := int64(seq*7919 + 1)
		rng := rand.New(rand.NewSource(seed))
		prod := New()
		ref := &refScheduler{tasks: map[string]*refState{}}
		log := []string{fmt.Sprintf("--- seq %d seed=%d ---", seq, seed)}

		steps := 8 + rng.Intn(10)
		for step := 0; step < steps; step++ {
			switch rng.Intn(3) {
			case 0, 1: // Add
				tk, expectValid := randomTask(rng)
				res, err := prod.Add(tk)
				rr := ref.add(toRef(tk))
				pr, pu := prodReason(err)

				// 与全排列可行性对拍。
				if pr == ReasonUnschedulable {
					all := append(ref.taskList(ref.order), toRef(tk))
					if len(all) <= 6 && feasiblePermutation(all) {
						t.Fatalf("[%s] Add %s rejected but a feasible permutation exists\n%s",
							t.Name(), describeTask(tk), strings.Join(log, "\n"))
					}
				}
				if pr == 0 {
					all := ref.taskList(prod.orderString())
					if len(all) <= 6 && !feasiblePermutation(all) {
						t.Fatalf("Add %s accepted but no feasible permutation exists\n%s",
							describeTask(tk), strings.Join(log, "\n"))
					}
					if res.Reordered && !refFeasible(all, refLimit) {
						t.Fatalf("Audsley order not feasible: %v\n%s", all, strings.Join(log, "\n"))
					}
				}

				basis := "insertion-kept-order"
				if res.Reordered {
					basis = "audsley-full-reorder"
				}
				if pr != 0 {
					basis = "rejected:" + reasonName(pr)
				}
				log = append(log, fmt.Sprintf(
					"Add %s -> ok=%v reordered=%v reason=%s(unassigned=%d) order=[%s] [basis:%s expectValid=%v]",
					describeTask(tk), err == nil, res.Reordered, reasonName(pr), pu,
					strings.Join(prod.Order(), ","), basis, expectValid))

				if pr != rr.errReason || pu != rr.unassigned || res.Reordered != rr.reordered {
					t.Fatalf("Add mismatch: prod(reason=%s,n=%d,reord=%v) ref(reason=%s,n=%d,reord=%v)\n%s",
						reasonName(pr), pu, res.Reordered,
						reasonName(rr.errReason), rr.unassigned, rr.reordered, strings.Join(log, "\n"))
				}
			case 2: // Remove
				id := fmt.Sprintf("t%d", rng.Intn(6))
				if rng.Intn(12) == 0 {
					id = ""
				}
				err := prod.Remove(id)
				rr := ref.remove(id)
				pr, _ := prodReason(err)
				log = append(log, fmt.Sprintf("Remove %q -> reason=%s order=[%s]",
					id, reasonName(pr), strings.Join(prod.Order(), ",")))
				if pr != rr {
					t.Fatalf("Remove mismatch: prod=%s ref=%s\n%s",
						reasonName(pr), reasonName(rr), strings.Join(log, "\n"))
				}
			}

			// 每步后核对 Order、缓存 Response、Order 任意前缀可调度。
			pOrder := prod.Order()
			if strings.Join(pOrder, ",") != strings.Join(ref.order, ",") {
				t.Fatalf("order mismatch: prod=%v ref=%v\n%s", pOrder, ref.order, strings.Join(log, "\n"))
			}
			prefix := make([]refTask, 0, len(pOrder))
			for _, id := range pOrder {
				got, err := prod.Response(id)
				if err != nil {
					t.Fatalf("Response(%s): %v\n%s", id, err, strings.Join(log, "\n"))
				}
				if want := ref.tasks[id].resp; got != want {
					t.Fatalf("Response(%s)=%d ref=%d\n%s", id, got, want, strings.Join(log, "\n"))
				}
				prefix = append(prefix, ref.tasks[id].task)
				if !refFeasible(prefix, refLimit) {
					t.Fatalf("prefix %v not independently schedulable\n%s", prefix, strings.Join(log, "\n"))
				}
			}
		}
		if testing.Verbose() {
			t.Log(strings.Join(log, "\n"))
		}
	}
}
