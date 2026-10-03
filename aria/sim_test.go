package aria

import (
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// naiveTx 是朴素模拟使用的事务记录。
type naiveTx struct {
	id  uint64
	ops []Op
}

// naiveRun 在 state 上朴素地执行一个事务（不修改 state），
// 返回 acc、写缓冲、读集与是否溢出失败。
func naiveRun(state []int64, ops []Op) (acc int64, writes map[int]int64, rs map[int]bool, failed bool) {
	writes = map[int]int64{}
	rs = map[int]bool{}
	for _, op := range ops {
		if op.Kind == OpRead {
			v, ok := writes[op.Key]
			if !ok {
				v = state[op.Key]
				rs[op.Key] = true
			}
			s := acc + v
			if acc > 0 && v > 0 && s < 0 || acc < 0 && v < 0 && s > 0 {
				return 0, nil, nil, true
			}
			acc = s
		} else {
			s := acc + op.D
			if acc > 0 && op.D > 0 && s < 0 || acc < 0 && op.D < 0 && s > 0 {
				return 0, nil, nil, true
			}
			writes[op.Key] = s
		}
	}
	return acc, writes, rs, false
}

// naiveBatch 是按题目规则逐步写成的朴素模拟：输入批起点状态与批内事务，
// 返回每个事务的结局与批后状态。它与 Executor 的实现相互独立，用于对照。
func naiveBatch(start []int64, batch []naiveTx) ([]Result, []int64) {
	type out struct {
		acc    int64
		writes map[int]int64
		rs     map[int]bool
		failed bool
	}
	// ① 并行阶段：每个事务都在批起点状态上试执行。
	outs := make([]out, len(batch))
	for i, tx := range batch {
		acc, writes, rs, failed := naiveRun(start, tx.ops)
		outs[i] = out{acc, writes, rs, failed}
	}
	// ② 预留表：逐键扫描全部未失败事务取最小事务号（中止者也计入）。
	wres := map[int]uint64{}
	rres := map[int]uint64{}
	for i, tx := range batch {
		if outs[i].failed {
			continue
		}
		for k := range outs[i].writes {
			if cur, ok := wres[k]; !ok || tx.id < cur {
				wres[k] = tx.id
			}
		}
		for k := range outs[i].rs {
			if cur, ok := rres[k]; !ok || tx.id < cur {
				rres[k] = tx.id
			}
		}
	}
	commit := make([]bool, len(batch))
	for i, tx := range batch {
		if outs[i].failed {
			continue
		}
		var waw, raw, war bool
		for k := range outs[i].writes {
			if w, ok := wres[k]; ok && w < tx.id {
				waw = true
			}
			if r, ok := rres[k]; ok && r < tx.id {
				war = true
			}
		}
		for k := range outs[i].rs {
			if w, ok := wres[k]; ok && w < tx.id {
				raw = true
			}
		}
		commit[i] = !waw && !(raw && war)
	}
	// ③ 提交者按事务号升序写入，其余按事务号升序串行重执行并立即写入。
	state := append([]int64(nil), start...)
	results := make([]Result, len(batch))
	for i, tx := range batch {
		switch {
		case outs[i].failed:
			results[i] = Result{TxID: tx.id, Phase: PhaseFailed}
		case commit[i]:
			for k, v := range outs[i].writes {
				state[k] = v
			}
			results[i] = Result{TxID: tx.id, Phase: PhaseParallel, Acc: outs[i].acc, HasAcc: true}
		}
	}
	for i, tx := range batch {
		if outs[i].failed || commit[i] {
			continue
		}
		acc, writes, _, failed := naiveRun(state, tx.ops)
		if failed {
			results[i] = Result{TxID: tx.id, Phase: PhaseFailed}
			continue
		}
		for k, v := range writes {
			state[k] = v
		}
		results[i] = Result{TxID: tx.id, Phase: PhaseFallback, Acc: acc, HasAcc: true}
	}
	return results, state
}

// checkSerialPermutations 对不超过 6 个未失败事务的批枚举全部串行次序，
// 要求至少一个次序的终态与每个事务的 acc 同时与实际一致。
func checkSerialPermutations(t *testing.T, start []int64, batch []naiveTx, got []Result, finalState []int64) {
	t.Helper()
	var alive []int
	for i, r := range got {
		if r.Phase != PhaseFailed {
			alive = append(alive, i)
		}
	}
	if len(alive) == 0 || len(alive) > 6 {
		return
	}
	actualAcc := map[uint64]int64{}
	for _, i := range alive {
		actualAcc[batch[i].id] = got[i].Acc
	}
	perm := make([]int, len(alive))
	used := make([]bool, len(alive))
	found := false
	var visit func(pos int)
	visit = func(pos int) {
		if found {
			return
		}
		if pos == len(alive) {
			state := append([]int64(nil), start...)
			accs := map[uint64]int64{}
			for _, ai := range perm {
				tx := batch[alive[ai]]
				acc, writes, _, failed := naiveRun(state, tx.ops)
				if failed {
					return // 该次序下溢出，不是与实际一致的次序
				}
				for k, v := range writes {
					state[k] = v
				}
				accs[tx.id] = acc
			}
			if !reflect.DeepEqual(state, finalState) {
				return
			}
			for id, a := range actualAcc {
				if accs[id] != a {
					return
				}
			}
			found = true
			return
		}
		for j := range alive {
			if used[j] {
				continue
			}
			used[j] = true
			perm[pos] = j
			visit(pos + 1)
			used[j] = false
		}
	}
	visit(0)
	if !found {
		t.Errorf("枚举全部 %d 个未失败事务的串行次序，无一与实际终态及 acc 一致", len(alive))
	}
}

// randomOps 生成 1 到 8 条随机操作，d 取小值避免随机场景溢出。
func randomOps(rng *rand.Rand, k int) []Op {
	n := 1 + rng.Intn(MaxOps)
	ops := make([]Op, n)
	for i := range ops {
		key := rng.Intn(k)
		if rng.Intn(2) == 0 {
			ops[i] = R(key)
		} else {
			ops[i] = W(key, int64(rng.Intn(21)-10))
		}
	}
	return ops
}

// 与朴素模拟对照 2000 组随机批次，日志打印输入、输出与判定依据。
func TestRandomBatchesAgainstNaiveSim(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	for iter := 0; iter < 2000; iter++ {
		k := 1 + rng.Intn(6)
		bsz := 1 + rng.Intn(4)
		nTx := rng.Intn(2*bsz + 2)
		e, err := New(k, bsz)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("=== 第 %d 组: K=%d Bsz=%d 事务数=%d", iter, k, bsz, nTx)
		var queue []naiveTx
		for j := 0; j < nTx; j++ {
			ops := randomOps(rng, k)
			id, err := e.Submit(ops)
			if err != nil {
				t.Fatalf("Submit(%v) 被拒: %v", ops, err)
			}
			queue = append(queue, naiveTx{id: id, ops: ops})
			t.Logf("输入 T%d: %v", id, ops)
		}
		simState := make([]int64, k)
		for len(queue) > 0 {
			n := min(len(queue), bsz)
			batch := queue[:n]
			queue = queue[n:]
			opsTotal := 0
			for _, tx := range batch {
				opsTotal += len(tx.ops)
			}
			start := append([]int64(nil), simState...)
			got := e.RunBatch()
			want, nextState := naiveBatch(start, batch)
			simState = nextState
			// 逐事务对照阶段与 acc，并打印判定依据。
			if len(got) != len(batch) {
				t.Fatalf("第 %d 组：批结果数=%d，预期 %d", iter, len(got), len(batch))
			}
			for i, tx := range batch {
				g, w := got[i], want[i]
				t.Logf("输出 T%d: 阶段=%s acc=%d ok=%v 依据: %s", tx.id, g.Phase, g.Acc, g.HasAcc, g.Reason)
				if g.TxID != tx.id {
					t.Fatalf("第 %d 组：结果[%d] 事务号=%d，预期 %d", iter, i, g.TxID, tx.id)
				}
				if g.Phase != w.Phase || g.HasAcc != w.HasAcc || (w.HasAcc && g.Acc != w.Acc) {
					t.Errorf("第 %d 组 T%d：实际(%s,%d,%v) 朴素模拟(%s,%d,%v)",
						iter, tx.id, g.Phase, g.Acc, g.HasAcc, w.Phase, w.Acc, w.HasAcc)
				}
			}
			// 批内事务号最小且未失败者必在并行阶段提交。
			for i := range got {
				if got[i].Phase != PhaseFailed {
					if got[i].Phase != PhaseParallel {
						t.Errorf("第 %d 组：最小未失败者 T%d 阶段=%s，预期并行",
							iter, got[i].TxID, got[i].Phase)
					}
					break
				}
			}
			// 预留表触及表项数不超过批内操作总数的两倍。
			if e.lastTouches > 2*opsTotal {
				t.Errorf("第 %d 组：预留表触及 %d 项 > 2×%d", iter, e.lastTouches, opsTotal)
			}
			// 终态与朴素模拟一致。
			if gotState := stateOf(t, e, k); !reflect.DeepEqual(gotState, simState) {
				t.Fatalf("第 %d 组：批后状态=%v，朴素模拟=%v", iter, gotState, simState)
			}
			// 不超过 6 个未失败事务的批：枚举全部串行次序对照。
			checkSerialPermutations(t, start, batch, got, simState)
		}
		if e.Pending() != 0 {
			t.Fatalf("第 %d 组：结束后 Pending=%d", iter, e.Pending())
		}
	}
}

// Submit 与 RunBatch 并发调用：事务号唯一递增、每个事务恰好一个结局、
// 结果等价于某个串行顺序（由互斥串行化保证，竞态检测器验证安全性）。
func TestConcurrentSubmitAndRunBatch(t *testing.T) {
	e := mustNew(t, 8, 4)
	const producers = 8
	const perProducer = 50
	const total = producers * perProducer
	var wg sync.WaitGroup
	ids := make([][]uint64, producers)
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				id, err := e.Submit([]Op{R(p % 8), W((p+1)%8, int64(i%7))})
				if err != nil {
					t.Errorf("并发 Submit 被拒: %v", err)
					return
				}
				ids[p] = append(ids[p], id)
			}
		}(p)
	}
	var collected atomic.Int64
	results := make([][]Result, 2)
	for c := 0; c < 2; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for collected.Load() < total {
				res := e.RunBatch()
				if len(res) == 0 {
					continue
				}
				results[c] = append(results[c], res...)
				collected.Add(int64(len(res)))
			}
		}(c)
	}
	// 并发读者。
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for collected.Load() < total {
				_, _ = e.Value(0)
				_ = e.Pending()
			}
		}()
	}
	wg.Wait()
	// 事务号恰好为 1..total 且唯一。
	seen := make([]bool, total+1)
	for p := range ids {
		for _, id := range ids[p] {
			if id < 1 || id > total || seen[id] {
				t.Fatalf("事务号 %d 越界或重复", id)
			}
			seen[id] = true
		}
	}
	// 每个事务恰好落入一个结局。
	gotByID := make([]int, total+1)
	for c := range results {
		for _, r := range results[c] {
			gotByID[r.TxID]++
		}
	}
	for id := 1; id <= total; id++ {
		if gotByID[id] != 1 {
			t.Fatalf("T%d 的结局数=%d，预期恰好 1", id, gotByID[id])
		}
	}
	if e.Pending() != 0 {
		t.Fatalf("结束后 Pending=%d，预期 0", e.Pending())
	}
}
