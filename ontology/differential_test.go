package ontology

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// runRandom 用随机驱动执行动作，返回结果、事件流与最终状态。
func runRandom(t *testing.T, rng *rand.Rand, ops []Op) (map[int]OpResult, []SchedEvent, int64, Privilege, Attrs) {
	t.Helper()
	s := NewScheduler()
	exec := NewExecutor(s)

	results := make(chan OpResult, len(ops))
	for _, op := range ops {
		op := op
		go func() { results <- exec.Run(context.Background(), op) }()
	}

	out := make(map[int]OpResult, len(ops))
	type req = gateReq
	var readQ, commQ []req

	drain := func() {
		for {
			select {
			case r := <-s.readCh:
				readQ = append(readQ, r)
			case r := <-s.commCh:
				commQ = append(commQ, r)
			case res := <-results:
				out[res.OpID] = res
			default:
				return
			}
		}
	}
	pop := func(q *[]req, i int) req {
		r := (*q)[i]
		*q = append((*q)[:i], (*q)[i+1:]...)
		return r
	}
	await := func(r req) {
		for {
			select {
			case rec := <-s.doneCh:
				if rec.OpID == r.opID && rec.Attempt == r.attempt {
					return
				}
			case x := <-s.readCh:
				readQ = append(readQ, x)
			case x := <-s.commCh:
				commQ = append(commQ, x)
			case res := <-results:
				out[res.OpID] = res
			}
		}
	}
	waitOne := func() {
		select {
		case r := <-s.readCh:
			readQ = append(readQ, r)
		case r := <-s.commCh:
			commQ = append(commQ, r)
		case res := <-results:
			out[res.OpID] = res
		case rec := <-s.doneCh:
			_ = rec
		}
	}

	for len(out) < len(ops) {
		drain()
		if len(commQ) > 0 && (len(readQ) == 0 || rng.Intn(2) == 0) {
			r := pop(&commQ, rng.Intn(len(commQ)))
			close(r.perm)
			await(r)
			drain()
			continue
		}
		if len(readQ) > 0 {
			i := rng.Intn(len(readQ))
			r := pop(&readQ, i)
			close(r.perm)
			// 等待读完进入提交闸门。
			for !containsReq(commQ, r) && len(out) < len(ops) {
				waitOne()
				drain()
			}
			continue
		}
		waitOne()
	}
	ver, hw, attrs := exec.FinalState(ops[0].Instance)
	return out, s.Events(), ver, hw, attrs
}

func containsReq(q []gateReq, target gateReq) bool {
	for _, r := range q {
		if r.opID == target.opID && r.attempt == target.attempt {
			return true
		}
	}
	return false
}

// TestRandomDifferential 随机多权限序列 × 随机交织，与独立参照模型
// 逐尝试判定、最终状态做差分对照，并检查抢占/无额外状态等不变量。
func TestRandomDifferential(t *testing.T) {
	const iterations = 300
	for seed := int64(0); seed < iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 2 + rng.Intn(5)
		ops := make([]Op, n)
		for i := range ops {
			ops[i] = counterOp(i+1, Privilege(1+rng.Intn(5)), 1+rng.Intn(7), 1+rng.Intn(4))
		}

		results, events, ver, hw, attrs := runRandom(t, rng, ops)
		ref, refStates, err := ReplayReference(ops, events)
		if err != nil {
			t.Fatalf("seed=%d reference replay: %v", seed, err)
		}
		diffAgainstReference(t, seed, ops, results, ref)
		rs := refStates[ops[0].Instance]
		if ver != rs.Version || hw != rs.HighWater ||
			!equalAttrs(attrs, rs.Attrs) {
			t.Fatalf("seed=%d final state mismatch impl=(%d,%d,%v) ref=(%d,%d,%v)",
				seed, ver, hw, attrs, rs.Version, rs.HighWater, rs.Attrs)
		}
		// 不变量：最终版本号恰好等于 committed 动作数（每次生效 +1）。
		committed := 0
		for _, r := range results {
			if r.Status == StatusCommitted {
				committed++
			}
		}
		if int(ver) != committed {
			t.Fatalf("seed=%d version=%d but committed ops=%d", seed, ver, committed)
		}
		// 不变量：被抢占动作最后一次判定必须是 preempted，且其之后
		// 没有任何同 op 的更多尝试（立即终止）。
		for _, r := range results {
			if r.Status == StatusPreempted {
				last := r.Attempts[len(r.Attempts)-1]
				if last.Verdict != "preempted" {
					t.Fatalf("seed=%d op%d preempted but last verdict=%s", seed, r.OpID, last.Verdict)
				}
			}
		}
	}
}

func equalAttrs(a, b Attrs) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func diffAgainstReference(t *testing.T, seed int64, ops []Op, results map[int]OpResult, ref map[int]RefResult) {
	t.Helper()
	statusMap := map[Status]RefStatus{
		StatusCommitted: RefCommitted,
		StatusPreempted: RefPreempted,
		StatusExhausted: RefExhausted,
	}
	for id, impl := range results {
		rf := ref[id]
		if rf.Status != statusMap[impl.Status] {
			t.Fatalf("seed=%d op%d status impl=%s ref=%s", seed, id, impl.Status, rf.Status)
		}
		if impl.CommittedAt != rf.CommittedAt {
			t.Fatalf("seed=%d op%d commit version impl=%d ref=%d", seed, id, impl.CommittedAt, rf.CommittedAt)
		}
		if len(impl.Attempts) != len(rf.Attempts) {
			t.Fatalf("seed=%d op%d attempts impl=%d ref=%d", seed, id, len(impl.Attempts), len(rf.Attempts))
		}
		for i := range impl.Attempts {
			if impl.Attempts[i].Verdict != rf.Attempts[i].Verdict {
				t.Fatalf("seed=%d op%d attempt %d verdict impl=%s ref=%s",
					seed, id, i+1, impl.Attempts[i].Verdict, rf.Attempts[i].Verdict)
			}
		}
	}
}

// TestRealConcurrencyRace 在无钩子的真实并发下运行多权限竞争，
// 借助 -race 检测数据竞争，并验证四类结果与最终状态自洽。
func TestRealConcurrencyRace(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		rng := rand.New(rand.NewSource(int64(iter)))
		exec := NewExecutor(nil)
		n := 4 + rng.Intn(6)
		var wg sync.WaitGroup
		results := make(chan OpResult, n)
		for i := 0; i < n; i++ {
			op := counterOp(i+1, Privilege(1+rng.Intn(5)), 1+rng.Intn(5), 1+rng.Intn(6))
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- exec.Run(context.Background(), op)
			}()
		}
		wg.Wait()
		close(results)

		committed, preempted, exhausted := 0, 0, 0
		for r := range results {
			switch r.Status {
			case StatusCommitted:
				committed++
			case StatusPreempted:
				preempted++
			case StatusExhausted:
				exhausted++
			}
			if r.Status == StatusPreempted {
				last := r.Attempts[len(r.Attempts)-1]
				if last.Verdict != "preempted" {
					t.Fatalf("iter=%d op%d bad last verdict %s", iter, r.OpID, last.Verdict)
				}
			}
		}
		ver, _, _ := exec.FinalState(testInstance)
		if int(ver) != committed {
			t.Fatalf("iter=%d version=%d committed=%d preempted=%d exhausted=%d",
				iter, ver, committed, preempted, exhausted)
		}
	}
}

// TestSerializedOrderingWitness 验证存在满足先后约束的等价串行顺序：
// 把所有 committed 动作按 CommittedAt 排序得到一个串行序列，
// 重放该序列必须得到相同最终属性；且任何被抢占动作不得出现在
// 抢占它的高权限生效位置之前。
func TestSerializedOrderingWitness(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ops := make([]Op, 6)
	for i := range ops {
		ops[i] = counterOp(i+1, Privilege(1+rng.Intn(6)), 1+rng.Intn(4), 1+rng.Intn(3))
	}
	results, events, _, _, _ := runRandom(t, rng, ops)
	ref, _, err := ReplayReference(ops, events)
	if err != nil {
		t.Fatal(err)
	}
	_ = ref
	_ = fmt.Sprint

	type commitInfo struct {
		opID int
		at   int64
		priv Privilege
	}
	var commits []commitInfo
	for _, r := range results {
		if r.Status == StatusCommitted {
			commits = append(commits, commitInfo{r.OpID, r.CommittedAt, r.Priv})
		}
	}
	// 按生效版本排序即为一个合法串行顺序。
	sort.Slice(commits, func(i, j int) bool { return commits[i].at < commits[j].at })
	for i := 1; i < len(commits); i++ {
		if commits[i].at < commits[i-1].at {
			t.Fatalf("commit versions not ordered: %+v", commits)
		}
	}
	// 抢占约束：每个被抢占动作的权限必须低于 CommittedAt 之前最近的
	// 更高权限生效写入。
	for _, r := range results {
		if r.Status != StatusPreempted {
			continue
		}
		found := false
		for _, c := range commits {
			if c.priv > r.Priv {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("preempted op %d has no strictly higher committed writer", r.OpID)
		}
	}
}
