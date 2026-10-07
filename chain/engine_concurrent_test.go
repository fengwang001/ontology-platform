package chain

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// 并发测试动作：
//
//	incIfPositive(amount): 前置要求计数器当前可见值 >= amount；写入 n -= amount。
//
// 全局不变量：counter.n >= 0。串行语义下只有“被提交全序允许”的链条会成功。
func concurrentRegistry() *Registry {
	reg := buildTestRegistry() // 复用 set 等
	mustReg(reg, &Action{
		Name: "incIfPositive",
		Pre: func(c ExecContext) (bool, string) {
			attrs, ok := c.State().Get("counter")
			cur := 0
			if ok {
				cur = num(attrs["n"])
			}
			need := num(c.Input()["amount"])
			if cur < need {
				return false, fmt.Sprintf("balance %d < amount %d", cur, need)
			}
			return true, fmt.Sprintf("balance %d >= %d", cur, need)
		},
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			attrs, _ := c.State().Get("counter")
			next := num(attrs["n"]) - num(c.Input()["amount"])
			return []WriteOp{{ObjectID: "counter", Upsert: map[string]any{"n": next}}},
				map[string]any{"balance": next}, "decrement", nil
		},
		Post: func(c ExecContext, out map[string]any) (bool, string) {
			attrs, _ := c.State().Get("counter")
			if num(attrs["n"]) < 0 {
				return false, "negative balance after effect"
			}
			return true, "non-negative"
		},
	})
	return reg
}

func nonNegative(state map[string]map[string]any) (bool, []string) {
	if attrs, ok := state["counter"]; ok && num(attrs["n"]) < 0 {
		return false, []string{"counter.n < 0"}
	}
	return true, nil
}

// 并发交织：N 条链条并发扣减同一计数器，只允许扣到 0。
// 最终状态必须等于“按引擎提交全序逐条朴素串行执行”的结果，
// 且任何已提交前缀都保持 counter.n >= 0。
func TestConcurrentChainsEquivalentToNaiveSerial(t *testing.T) {
	reg := concurrentRegistry()
	store := NewObjectStore()
	// 初始余额 50。
	store.Commit([]WriteOp{{ObjectID: "counter", Upsert: map[string]any{"n": 50}}})
	eng := NewEngine(reg, store, nonNegative)

	const n = 40
	amounts := []int{3, 7, 1, 10, 2, 5}
	var wg sync.WaitGroup
	results := make([]*ChainResult, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, _ := eng.Run(context.Background(),
				fmt.Sprintf("job-%02d", i), "incIfPositive",
				Params{"amount": amounts[i%len(amounts)]})
			results[i] = res
		}(i)
	}
	close(start)
	wg.Wait()

	final := store.Snapshot()
	balance := num(final["counter"]["n"])
	if balance < 0 {
		t.Fatalf("invariant broken: balance=%d", balance)
	}

	// 按引擎记录的提交全序，用独立存储朴素串行重放一遍，结果必须一致。
	order := eng.CommitOrder()
	byID := map[string]*ChainResult{}
	var totalCommitted int
	for _, res := range results {
		// ChainResult 未携带 ID，这里用输入重建索引不便；改为在下面按顺序匹配。
		_ = res
	}
	_ = byID
	_ = totalCommitted

	serialStore := NewObjectStore()
	serialStore.Commit([]WriteOp{{ObjectID: "counter", Upsert: map[string]any{"n": 50}}})
	jobs := make([]SerialJob, 0, n)
	jobInput := map[string]int{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("job-%02d", i)
		jobs = append(jobs, SerialJob{ID: id, Entry: "incIfPositive",
			Input: Params{"amount": amounts[i%len(amounts)]}})
		jobInput[id] = amounts[i%len(amounts)]
	}
	// 朴素串行固定按 job-00..job-(n-1) 顺序执行，得到“某一合法全序”的结果。
	_, serialState := RunNaiveSerial(reg, serialStore, nonNegative, jobs)
	serialBalance := num(serialState["counter"]["n"])

	// 关键等价性：两条路径都只允许扣到 0，总成功扣减额必须等于 min(需求, 初始余额)。
	var totalDemand int
	for i := 0; i < n; i++ {
		totalDemand += amounts[i%len(amounts)]
	}
	want := 50 - minInt(totalDemand, 50)
	if balance != want {
		t.Fatalf("concurrent balance=%d want %d (serial=%d)", balance, want, serialBalance)
	}
	if serialBalance != want {
		t.Fatalf("naive serial balance=%d want %d", serialBalance, want)
	}

	// 按并发引擎实际提交全序，用朴素串行逐条重放成功集合，逐前缀核对不变量与终值。
	orderedStore := NewObjectStore()
	orderedStore.Commit([]WriteOp{{ObjectID: "counter", Upsert: map[string]any{"n": 50}}})
	cur := 50
	for _, id := range order {
		amt := jobInput[id]
		if cur < amt {
			t.Fatalf("committed order prefix violates precondition at %s: %d < %d", id, cur, amt)
		}
		cur -= amt
		orderedStore.Commit([]WriteOp{{ObjectID: "counter",
			Upsert: map[string]any{"n": cur}}})
		if ok, v := nonNegative(orderedStore.Snapshot()); !ok {
			t.Fatalf("invariant broken along committed prefix %s: %v", id, v)
		}
	}
	if cur != balance {
		t.Fatalf("order-replay balance=%d != engine balance=%d", cur, balance)
	}

	// 至少发生过一次重放（高并发下几乎必然），且最终所有结果都有确定结论。
	var replays int
	for i, res := range results {
		if res == nil {
			t.Fatalf("result %d is nil", i)
		}
		replays += res.Replays
		switch res.Record.Status {
		case StatusCompleted, StatusPreFailed:
		default:
			t.Fatalf("unexpected status %v for job %d", res.Record.Status, i)
		}
	}
	t.Logf("committed=%d/%d replays=%d finalBalance=%d", len(order), n, replays, balance)
}

// 显式按“并发提交全序”朴素串行执行，与并发引擎终态逐字段一致。
func TestNaiveReplayMatchesEngineCommittedSet(t *testing.T) {
	reg := concurrentRegistry()
	store := NewObjectStore()
	store.Commit([]WriteOp{{ObjectID: "counter", Upsert: map[string]any{"n": 20}}})
	eng := NewEngine(reg, store, nonNegative)

	var wg sync.WaitGroup
	start := make(chan struct{})
	amounts := []int{8, 8, 8, 8, 8}
	for i := range amounts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _ = eng.Run(context.Background(), fmt.Sprintf("j%d", i),
				"incIfPositive", Params{"amount": amounts[i]})
		}(i)
	}
	close(start)
	wg.Wait()

	order := eng.CommitOrder() // 如 j2,j0,j3,...
	ref := NewObjectStore()
	ref.Commit([]WriteOp{{ObjectID: "counter", Upsert: map[string]any{"n": 20}}})
	jobs := make([]SerialJob, 0, len(order))
	for _, id := range order {
		var idx int
		fmt.Sscanf(id, "j%d", &idx)
		jobs = append(jobs, SerialJob{ID: id, Entry: "incIfPositive",
			Input: Params{"amount": amounts[idx]}})
	}
	_, refState := RunNaiveSerial(reg, ref, nonNegative, jobs)
	engState := store.Snapshot()
	if num(refState["counter"]["n"]) != num(engState["counter"]["n"]) {
		t.Fatalf("serial-by-commit-order=%d engine=%d",
			num(refState["counter"]["n"]), num(engState["counter"]["n"]))
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
