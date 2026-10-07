package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// 并发压力测试：大量随机交织的并发调用结束后，
//  1. 被接受的调用集合按提交序号重放到独立的朴素串行实现上，
//     最终对象状态、版本号、历史序列必须完全一致；
//  2. 动作级不变量（账户总额守恒、无负余额）在最终状态成立；
//  3. 不存在“各自通过前置校验、组合后违反不变量”的被接受调用对。
func TestConcurrentExecutionsEquivalentToSerialOrder(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 2026} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			runConcurrencyTrial(t, seed)
		})
	}
}

func runConcurrencyTrial(t *testing.T, seed int64) {
	t.Helper()
	const (
		accounts   = 8
		goroutines = 12
		callsEach  = 40
		initBal    = int64(100)
	)
	store := NewStore()
	reg := NewRegistry()
	mustRegister(t, reg, transferAction())
	seedAccounts(store, accounts, initBal)
	exec := NewExecutor(store, reg)
	// 生成确定性的随机调用序列，再随机交织地并发发出。
	rng := rand.New(rand.NewSource(seed))
	calls := make([]Call, 0, goroutines*callsEach)
	for g := 0; g < goroutines; g++ {
		for i := 0; i < callsEach; i++ {
			from := rng.Intn(accounts)
			to := rng.Intn(accounts)
			for to == from {
				to = rng.Intn(accounts)
			}
			amount := int64(1 + rng.Intn(60))
			calls = append(calls, transferCall(
				fmt.Sprintf("g%02d-c%02d", g, i), accountID(from), accountID(to), amount))
		}
	}
	results := make([]Result, len(calls))
	var wg sync.WaitGroup
	// 按随机置换交织发出，放大并发冲突面。
	order := rand.New(rand.NewSource(seed + 1)).Perm(len(calls))
	for _, idx := range order {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = exec.Execute(calls[i])
		}(idx)
	}
	wg.Wait()
	// 收集被接受的调用，按提交序号（全序）排序。
	type acceptedCall struct {
		call Call
		seq  int64
	}
	var accepted []acceptedCall
	rejections := map[RejectCategory]int{}
	for i, res := range results {
		if res.Accepted() {
			accepted = append(accepted, acceptedCall{calls[i], res.CommitSeq})
			continue
		}
		if res.Status != StatusRejected {
			t.Fatalf("call %s errored: %v", calls[i].ID, res.Err)
		}
		rejections[res.Reject.Category]++
		// 并发场景下只允许前置失败与并发撤销两类拒绝。
		if res.Reject.Category != RejectPrecondition &&
			res.Reject.Category != RejectConcurrentInvalidation {
			t.Fatalf("call %s rejected with unexpected category %v",
				calls[i].ID, res.Reject.Category)
		}
	}
	if len(accepted) == 0 {
		t.Fatalf("no call accepted; test is vacuous")
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].seq < accepted[j].seq })
	for i, a := range accepted {
		if a.seq != int64(i+1) {
			t.Fatalf("commit seqs not contiguous: position %d has seq %d", i, a.seq)
		}
	}
	// 用独立的朴素串行实现按全序重放被接受的调用。
	naive := NewNaiveExecutor(reg)
	for i := 0; i < accounts; i++ {
		naive.Seed(Object{ID: accountID(i), Type: "account", Props: map[string]any{"balance": initBal}})
	}
	for _, a := range accepted {
		res := naive.Execute(a.call)
		if !res.Accepted() {
			t.Fatalf("call %s accepted by concurrent engine but rejected in serial replay: %+v",
				a.call.ID, res.Reject)
		}
	}
	// 对照最终对象状态（属性与版本号）。
	for i := 0; i < accounts; i++ {
		id := accountID(i)
		gotObj, gotOK := store.GetObject(id)
		wantObj, wantOK := naive.GetObject(id)
		if gotOK != wantOK {
			t.Fatalf("object %s existence mismatch: concurrent=%v serial=%v", id, gotOK, wantOK)
		}
		if !gotOK {
			continue
		}
		if gotObj.Version != wantObj.Version {
			t.Fatalf("object %s version: concurrent=%d serial=%d", id, gotObj.Version, wantObj.Version)
		}
		if gotObj.Props["balance"] != wantObj.Props["balance"] {
			t.Fatalf("object %s balance: concurrent=%v serial=%v",
				id, gotObj.Props["balance"], wantObj.Props["balance"])
		}
		// 历史序列一致。
		gotHist, wantHist := store.ObjectHistory(id), naive.ObjectHistory(id)
		if len(gotHist) != len(wantHist) {
			t.Fatalf("object %s history length: concurrent=%d serial=%d", id, len(gotHist), len(wantHist))
		}
		for j := range gotHist {
			if gotHist[j] != wantHist[j] {
				t.Fatalf("object %s history[%d]: concurrent=%+v serial=%+v", id, j, gotHist[j], wantHist[j])
			}
		}
	}
	gotAH, wantAH := store.ActionHistory("transfer"), naive.ActionHistory("transfer")
	if len(gotAH) != len(wantAH) {
		t.Fatalf("action history length: concurrent=%d serial=%d", len(gotAH), len(wantAH))
	}
	for j := range gotAH {
		if gotAH[j] != wantAH[j] {
			t.Fatalf("action history[%d]: concurrent=%+v serial=%+v", j, gotAH[j], wantAH[j])
		}
	}
	// 不变量：总额守恒、无负余额。
	var total int64
	for i := 0; i < accounts; i++ {
		bal := balanceOf(t, store, accountID(i))
		if bal < 0 {
			t.Fatalf("invariant violated: negative balance %d on %s", bal, accountID(i))
		}
		total += bal
	}
	if total != int64(accounts)*initBal {
		t.Fatalf("invariant violated: total balance %d, want %d", total, int64(accounts)*initBal)
	}
	t.Logf("seed=%d accepted=%d rejected=%v", seed, len(accepted), rejections)
}
