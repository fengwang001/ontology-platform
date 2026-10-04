package exec_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"

	"ontology/action"
	"ontology/exec"
)

func errLabel(e error) string {
	switch {
	case e == nil:
		return ""
	case errors.Is(e, exec.ErrInvalid):
		return "invalid"
	case errors.Is(e, exec.ErrNotFound):
		return "notfound"
	case errors.Is(e, exec.ErrExists):
		return "exists"
	case errors.Is(e, exec.ErrState):
		return "state"
	case errors.Is(e, exec.ErrStaleAttempt):
		return "stale"
	case errors.Is(e, exec.ErrNoSlot):
		return "noslot"
	default:
		return "other:" + e.Error()
	}
}

func TestNaiveModelDifferential(t *testing.T) {
	const cases = 1500
	for seed := int64(1); seed <= cases; seed++ {
		t.Run("seed-"+strconv.FormatInt(seed, 10), func(t *testing.T) {
			runOne(t, seed)
		})
	}
}

func runOne(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	M := 1 + rng.Intn(5)
	s := exec.New(M)
	md := newModel(M)

	digests := []string{"dA", "dB", "dC", "dD", "dE"}
	platforms := []action.Platform{
		plat(),
		plat("os", "linux"),
		plat("os", "win"),
		plat("os", "linux", "arch", "x86"),
		plat("arch", "arm"),
		plat("k", "v"),
	}
	workerNames := []string{"W1", "W2", "W3"}
	workerProps := []map[string]string{
		{"os": "linux", "arch": "x86"},
		{"os": "win"},
		{"os": "linux", "arch": "arm", "k": "v"},
		{},
	}

	var liveWaiters []int
	var trace []string

	compare := func(where string) {
		t.Helper()
		if msg := diffStates(s, md, liveWaiters); msg != "" {
			t.Fatalf("seed=%d mismatch @%s\n%s\n--- input/output trace ---\n%s\n--- model rationale ---\n%s",
				seed, where, msg, strings.Join(trace, "\n"), strings.Join(md.log, "\n"))
		}
	}

	n := 20 + rng.Intn(80)
	for i := 0; i < n; i++ {
		k := rng.Intn(7)
		switch k {
		case 0: // Register
			name := workerNames[rng.Intn(len(workerNames))]
			props := workerProps[rng.Intn(len(workerProps))]
			slots := 1 + rng.Intn(3)
			if rng.Intn(20) == 0 {
				slots = 65
			}
			got := dash(errLabel(s.Register(name, props, slots)))
			want := dash(md.register(name, props, slots))
			trace = append(trace, fmt.Sprintf("%d Register(%s,slots=%d) => %s | model %s", i, name, slots, dash(got), want))
			if got != dash(want) {
				t.Fatalf("seed=%d register mismatch: %s vs %s\n%s", seed, got, want, strings.Join(trace, "\n"))
			}
		case 1: // Execute
			d := digests[rng.Intn(len(digests))]
			p := platforms[rng.Intn(len(platforms))]
			prio := rng.Intn(11) // 10 为非法
			skip := rng.Intn(3) == 0
			idS, errS := s.Execute(d, p, prio, skip)
			idM, lblM := md.execute(d, p, prio, skip)
			trace = append(trace, fmt.Sprintf("%d Execute(%s,%v,prio=%d,skip=%v) => id=%d,%s | model id=%d,%s",
				i, d, p, prio, skip, idS, dash(errLabel(errS)), idM, dash(lblM)))
			if dash(errLabel(errS)) != dash(lblM) || (errS == nil && idS != idM) {
				t.Fatalf("seed=%d execute mismatch @%d", seed, i)
			}
			if errS == nil {
				liveWaiters = append(liveWaiters, idS)
			}
		case 2: // Poll
			name := workerNames[rng.Intn(len(workerNames))]
			dS, attS, errS := s.Poll(name)
			dM, attM, lblM := md.poll(name)
			got := errLabel(errS)
			if errS == nil && dS == "" {
				got = "empty"
			}
			if got == "" {
				got = "ok"
			}
			trace = append(trace, fmt.Sprintf("%d Poll(%s) => %s/%d,%s | model %s/%d,%s",
				i, name, dS, attS, dash(got), dM, attM, dash(lblM)))
			if got != dash(lblM) || dS != dM || attS != attM {
				t.Fatalf("seed=%d poll mismatch @%d: real %s/%d/%s vs model %s/%d/%s\n%s",
					seed, i, dS, attS, got, dM, attM, lblM, strings.Join(trace, "\n"))
			}
		case 3, 4: // Complete normal / infra
			infra := k == 4
			name := workerNames[rng.Intn(len(workerNames))]
			d := digests[rng.Intn(len(digests))]
			attempt := 1 + rng.Intn(4)
			exit := rng.Intn(5)
			got := dash(errLabel(s.Complete(name, d, attempt, exit, infra)))
			want := md.complete(name, d, attempt, exit, infra)
			trace = append(trace, fmt.Sprintf("%d Complete(%s,%s,att=%d,exit=%d,infra=%v) => %s | model %s",
				i, name, d, attempt, exit, infra, got, dash(want)))
			if got != dash(want) {
				t.Fatalf("seed=%d complete mismatch @%d: %s vs %s", seed, i, got, want)
			}
		case 5: // WorkerLost
			name := workerNames[rng.Intn(len(workerNames))]
			got := dash(errLabel(s.WorkerLost(name)))
			want := md.workerLost(name)
			trace = append(trace, fmt.Sprintf("%d WorkerLost(%s) => %s | model %s", i, name, got, dash(want)))
			if got != dash(want) {
				t.Fatalf("seed=%d workerlost mismatch @%d", seed, i)
			}
		case 6: // Cancel
			id := 0
			if len(liveWaiters) > 0 && rng.Intn(5) != 0 {
				id = liveWaiters[rng.Intn(len(liveWaiters))]
			} else if len(liveWaiters) > 0 {
				id = liveWaiters[len(liveWaiters)-1] + 1 + rng.Intn(3)
			}
			got := dash(errLabel(s.Cancel(id)))
			want := md.cancel(id)
			trace = append(trace, fmt.Sprintf("%d Cancel(%d) => %s | model %s", i, id, got, dash(want)))
			if got != dash(want) {
				t.Fatalf("seed=%d cancel mismatch @%d: %s vs %s", seed, i, got, want)
			}
		}
		compare(strconv.Itoa(i))
	}

	// 收尾：每个候选 platform 一个专用工作者（空平台放最后，避免它抢走具体平台的操作）；
	// 每轮先各自排空自己能匹配的操作，再全体失联，重复 M+1 轮把全部在途操作推到终局。
	order := drainOrder(len(platforms))
	for _, name := range workerNames {
		s.WorkerLost(name)
		md.workerLost(name)
	}
	for idx := range platforms {
		name := "drain" + strconv.Itoa(idx)
		s.WorkerLost(name)
		md.workerLost(name)
		if err := s.Register(name, drainProps(platforms[idx]), 64); err == nil {
			md.register(name, drainProps(platforms[idx]), 64)
		}
	}
	for round := 0; round < M+1; round++ {
		for idx := range platforms {
			name := "drain" + strconv.Itoa(idx)
			if err := s.Register(name, drainProps(platforms[idx]), 64); err == nil {
				md.register(name, drainProps(platforms[idx]), 64)
			}
		}
		for _, idx := range order {
			name := "drain" + strconv.Itoa(idx)
			for {
				d, _, perr := s.Poll(name)
				dM, _, _ := md.poll(name)
				lbl := errLabel(perr)
				if perr != nil && lbl != "noslot" {
					t.Fatalf("drain poll seed=%d round=%d: %s(%v)", seed, round, d, perr)
				}
				if d == "" && dM == "" {
					break
				}
				if d != dM {
					t.Fatalf("drain poll mismatch seed=%d: %s vs %s", seed, d, dM)
				}
			}
		}
		for idx := range platforms {
			s.WorkerLost("drain" + strconv.Itoa(idx))
			md.workerLost("drain" + strconv.Itoa(idx))
		}
	}
	compare("final-drain")

	for _, id := range liveWaiters {
		oS, okS := s.WaiterTerminal(id)
		mw := md.waiters[id]
		if !okS || mw == nil {
			t.Fatalf("seed=%d terminal presence mismatch waiter %d: real=%v model=%v\n%s", seed, id, okS, mw != nil, strings.Join(trace, "\n"))
		}
		if oS.Kind != mw.terminal {
			t.Fatalf("seed=%d waiter %d terminal: real=%s model=%s", seed, id, oS.Kind, mw.terminal)
		}
		if oS.Kind == "Result" || oS.Kind == "Cached" {
			if oS.Exit != mw.exit {
				t.Fatalf("seed=%d waiter %d exit: real=%d model=%d", seed, id, oS.Exit, mw.exit)
			}
		}
	}
	t.Logf("seed=%d M=%d steps=%d OK; %d waiters all terminal exactly once", seed, M, n, len(liveWaiters))
}

func dash(label string) string {
	if label == "" {
		return "ok"
	}
	return label
}

// drainProps 为平台要求生成一份包含其全部键值的工作者属性（空平台给出空属性）。
func drainProps(p action.Platform) map[string]string {
	out := map[string]string{}
	for k, v := range p {
		out[k] = v
	}
	return out
}

// drainOrder 让具体平台工作者先排空，空平台工作者最后，避免空平台把别的操作领走。
func drainOrder(n int) []int {
	var nonEmpty, empty []int
	for i := 0; i < n; i++ {
		if len(platformsList[i]) == 0 {
			empty = append(empty, i)
		} else {
			nonEmpty = append(nonEmpty, i)
		}
	}
	return append(nonEmpty, empty...)
}

var platformsList = []action.Platform{
	plat(),
	plat("os", "linux"),
	plat("os", "win"),
	plat("os", "linux", "arch", "x86"),
	plat("arch", "arm"),
	plat("k", "v"),
}

// diffStates 比较两侧的队列次序、在途摘要、缓存键、各等待者终局与槽位不变量。
func diffStates(s *exec.Scheduler, md *model, liveWaiters []int) string {
	var problems []string

	realQueue := s.QueueOrder()
	modelQueue := make([]string, 0, len(md.queued))
	for _, op := range md.sortedQueue() {
		modelQueue = append(modelQueue, op.digest)
	}
	if !equalStr(realQueue, modelQueue) {
		problems = append(problems, fmt.Sprintf("queue real=%v model=%v", realQueue, modelQueue))
	}

	realInflight := map[string]bool{}
	for _, d := range realQueue {
		realInflight[d] = true
	}
	modelInflight := map[string]bool{}
	for d := range md.ops {
		modelInflight[d] = true
	}
	// Assigned 的操作不在 queue 中，单独按等待者是否仍无终局 + 模型 ops 差集判定不可行；
	// 改用数量与槽位总和不变量，并用终局状态交叉验证。
	if s.UsedSlots() != s.AssignedCount() {
		problems = append(problems, "invariant: used slots != Assigned count")
	}

	// 每个等待者：真实已收到的终局必须与模型终局一致（模型终局非空时真实也必须可读到）。
	for _, id := range liveWaiters {
		mw := md.waiters[id]
		if mw == nil {
			continue
		}
		if mw.terminal == "" {
			if o, ok := s.WaiterTerminal(id); ok {
				problems = append(problems, fmt.Sprintf("waiter %d prematurely terminal real=%s model=alive", id, o.Kind))
			}
			continue
		}
		o, ok := s.WaiterTerminal(id)
		if !ok {
			problems = append(problems, fmt.Sprintf("waiter %d not terminal real, model=%s", id, mw.terminal))
			continue
		}
		if o.Kind != mw.terminal {
			problems = append(problems, fmt.Sprintf("waiter %d kind real=%s model=%s", id, o.Kind, mw.terminal))
		}
		if (o.Kind == "Result" || o.Kind == "Cached") && o.Exit != mw.exit {
			problems = append(problems, fmt.Sprintf("waiter %d exit real=%d model=%d", id, o.Exit, mw.exit))
		}
	}

	// Queued 集合一致性（队列摘要集）。
	rqSet := map[string]bool{}
	for _, d := range realQueue {
		rqSet[d] = true
	}
	mqSet := map[string]bool{}
	for d := range md.queued {
		mqSet[d] = true
	}
	if !sameSet(rqSet, mqSet) {
		problems = append(problems, fmt.Sprintf("queued set real=%v model=%v", keys(rqSet), keys(mqSet)))
	}

	// Assigned 数：模型侧统计。
	modelAssigned := 0
	modelUsedSlots := 0
	for _, op := range md.ops {
		if op.status == mAssigned {
			modelAssigned++
		}
	}
	for _, w := range md.workers {
		modelUsedSlots += len(w.held)
	}
	if s.AssignedCount() != modelAssigned {
		problems = append(problems, fmt.Sprintf("assigned count real=%d model=%d", s.AssignedCount(), modelAssigned))
	}
	if s.UsedSlots() != modelUsedSlots {
		problems = append(problems, fmt.Sprintf("used slots real=%d model=%d", s.UsedSlots(), modelUsedSlots))
	}

	if len(problems) == 0 {
		return ""
	}
	return strings.Join(problems, "\n")
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
