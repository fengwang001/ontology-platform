package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

type testOp struct {
	kind  string // "R" 读, "W" 写
	key   string
	value int64
}

type testScript struct {
	txs map[int64][]testOp // 键为从事务 1 起的时间戳
}

// genScript 用固定种子生成确定性脚本：每个事务一串读写。
func genScript(rng *rand.Rand, numTx, numKeys, maxOps int) *testScript {
	sc := &testScript{txs: map[int64][]testOp{}}
	for ts := 1; ts <= numTx; ts++ {
		n := 1 + rng.Intn(maxOps)
		ops := make([]testOp, 0, n)
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("k%d", rng.Intn(numKeys))
			if rng.Intn(2) == 0 {
				ops = append(ops, testOp{kind: "R", key: key})
			} else {
				ops = append(ops, testOp{kind: "W", key: key, value: rng.Int63n(1000) + 1})
			}
		}
		sc.txs[int64(ts)] = ops
	}
	return sc
}

type readObs struct {
	key   string
	value int64
}

type txOutcome struct {
	status      TxStatus
	reads       []readObs
	rejectedOps int
}

// runScript 执行脚本：concurrent=true 时每事务一个 goroutine 真正交错；
// 否则按时间戳 1..N 顺序串行执行。两种模式执行的是完全相同的输入脚本。
func runScript(t *testing.T, sc *testScript, concurrent bool) (*Scheduler, map[int64]*txOutcome) {
	t.Helper()
	s := newLoggedScheduler(t)
	numTx := len(sc.txs)
	tsOf := make([]int64, numTx)
	for i := range tsOf {
		tsOf[i] = s.Begin()
		if tsOf[i] != int64(i+1) {
			t.Fatalf("判定依据: 时间戳必须从 1 连续递增, 第 %d 个事务得到 %d", i+1, tsOf[i])
		}
	}

	outcomes := make([]*txOutcome, numTx)
	for i := range outcomes {
		outcomes[i] = &txOutcome{status: TxActive}
	}

	execute := func(idx int) {
		ts := tsOf[idx]
		oc := outcomes[idx]
		for _, op := range sc.txs[ts] {
			switch op.kind {
			case "R":
				v, err := s.Read(ts, op.key)
				switch {
				case err == nil:
					oc.reads = append(oc.reads, readObs{op.key, v})
				case isAbort(err):
					oc.status = TxAborted
				default:
					oc.rejectedOps++
				}
			case "W":
				err := s.Write(ts, op.key, op.value)
				switch {
				case isAbort(err):
					oc.status = TxAborted
				case err != nil:
					oc.rejectedOps++
				}
			}
			if oc.status == TxAborted {
				return
			}
		}
		_, err := s.Commit(ts)
		switch {
		case isAbort(err):
			oc.status = TxAborted
		case err != nil:
			oc.rejectedOps++
		default:
			oc.status = TxCommitted
		}
	}

	if concurrent {
		var wg sync.WaitGroup
		wg.Add(numTx)
		for i := 0; i < numTx; i++ {
			go func(idx int) { defer wg.Done(); execute(idx) }(i)
		}
		wg.Wait()
	} else {
		for i := 0; i < numTx; i++ {
			execute(i)
		}
	}

	result := map[int64]*txOutcome{}
	for i, oc := range outcomes {
		result[tsOf[i]] = oc
	}
	return s, result
}

func isAbort(err error) bool {
	var ae *AbortError
	return errors.As(err, &ae)
}

// token 是放行给某事务 worker 的一步：一次读写，或最后的提交。
type token struct {
	ts int64
	op *testOp // nil 表示提交
}

// mergeSchedule 生成一个保持「每个事务内部操作顺序」的全局交错时刻表：
// 把每个事务的 [ops..., COMMIT] 作为子序列随机归并。结果完全由 rng 决定。
func mergeSchedule(rng *rand.Rand, sc *testScript) []token {
	remaining := map[int64]int{}
	tss := make([]int64, 0, len(sc.txs))
	for ts, ops := range sc.txs {
		tss = append(tss, ts)
		remaining[ts] = len(ops) + 1
	}
	sort.Slice(tss, func(i, j int) bool { return tss[i] < tss[j] })

	sched := make([]token, 0)
	pos := map[int64]int{}
	for {
		ready := make([]int64, 0, len(tss))
		for _, ts := range tss {
			if remaining[ts] > 0 {
				ready = append(ready, ts)
			}
		}
		if len(ready) == 0 {
			return sched
		}
		ts := ready[rng.Intn(len(ready))]
		idx := pos[ts]
		pos[ts]++
		remaining[ts]--
		if idx < len(sc.txs[ts]) {
			op := sc.txs[ts][idx]
			sched = append(sched, token{ts: ts, op: &op})
		} else {
			sched = append(sched, token{ts: ts})
		}
	}
}

// runScheduled 按给定全局时刻表多 goroutine 并发执行：
// 协调器严格按时刻表把令牌投递给各事务 worker，故同时刻表 => 同线性顺序。
func runScheduled(t *testing.T, sched []token) (*Scheduler, map[int64]*txOutcome) {
	t.Helper()
	numTx := 0
	for _, tk := range sched {
		if int(tk.ts) > numTx {
			numTx = int(tk.ts)
		}
	}
	s := newLoggedScheduler(t)
	permits := make([]chan token, numTx+1)
	outcomes := make(map[int64]*txOutcome, numTx)
	for ts := 1; ts <= numTx; ts++ {
		if got := s.Begin(); got != int64(ts) {
			t.Fatalf("判定依据: 时间戳必须从 1 连续递增, 期望 %d 得到 %d", ts, got)
		}
		permits[ts] = make(chan token, 1)
		outcomes[int64(ts)] = &txOutcome{status: TxActive}
	}

	done := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for ts := 1; ts <= numTx; ts++ {
		wg.Add(1)
		go func(ts int64) {
			defer wg.Done()
			oc := outcomes[ts]
			dead := false
			for tk := range permits[ts] {
				if dead {
					done <- struct{}{}
					continue // 已终结后的剩余令牌直接排空，不调用调度器
				}
				if tk.op == nil {
					if _, err := s.Commit(ts); isAbort(err) {
						oc.status = TxAborted
					} else if err != nil {
						oc.rejectedOps++
					} else {
						oc.status = TxCommitted
					}
					dead = true
					done <- struct{}{}
					continue
				}
				op := *tk.op
				if op.kind == "R" {
					v, err := s.Read(ts, op.key)
					switch {
					case err == nil:
						oc.reads = append(oc.reads, readObs{op.key, v})
					case isAbort(err):
						oc.status = TxAborted
						dead = true
					default:
						oc.rejectedOps++
					}
				} else {
					err := s.Write(ts, op.key, op.value)
					switch {
					case isAbort(err):
						oc.status = TxAborted
						dead = true
					case err != nil:
						oc.rejectedOps++
					}
				}
				done <- struct{}{}
			}
		}(int64(ts))
	}

	// 协调器：严格按全局时刻表推进。每投出一步都同步等待该步执行完毕，
	// 因此时刻表本身就是所有调度器调用的唯一线性化顺序，与 goroutine 调度无关。
	for _, tk := range sched {
		permits[tk.ts] <- tk
		<-done
	}
	for ts := 1; ts <= numTx; ts++ {
		close(permits[ts])
	}
	wg.Wait()
	return s, outcomes
}

func statusSummary(m map[int64]*txOutcome) map[TxStatus]int {
	out := map[TxStatus]int{}
	for _, oc := range m {
		out[oc.status]++
	}
	return out
}

// serialReference 构造「已提交事务按时间戳升序串行执行」参照：
// 每个事务按操作顺序执行（缓冲写对自身读可见），提交时其全部缓冲写整体生效；
// 返回参照终值与每个已提交事务首次读各键时应得的值。
func serialReference(sc *testScript, outcomes map[int64]*txOutcome) (map[string]int64, map[int64]map[string]int64) {
	values := map[string]int64{}
	reads := map[int64]map[string]int64{}
	tss := make([]int64, 0, len(sc.txs))
	for ts := range sc.txs {
		tss = append(tss, ts)
	}
	sort.Slice(tss, func(i, j int) bool { return tss[i] < tss[j] })
	for _, ts := range tss {
		if outcomes[ts].status != TxCommitted {
			continue
		}
		buf := map[string]int64{}
		expectedReads := map[string]int64{}
		for _, op := range sc.txs[ts] {
			switch {
			case op.kind == "W":
				buf[op.key] = op.value
			case op.kind == "R":
				if v, own := buf[op.key]; own {
					if _, recorded := expectedReads[op.key]; !recorded {
						expectedReads[op.key] = v
					}
				} else if _, recorded := expectedReads[op.key]; !recorded {
					expectedReads[op.key] = values[op.key] // 键缺席时取初值 0
				}
			}
		}
		for k, v := range buf {
			values[k] = v
		}
		reads[ts] = expectedReads
	}
	return values, reads
}

// checkAgainstReference 用串行参照核对一次执行的终值与每个已提交事务的逐读值。
func checkAgainstReference(t *testing.T, tag string, iter int, s *Scheduler, sc *testScript, outcomes map[int64]*txOutcome) {
	t.Helper()
	for ts, oc := range outcomes {
		if oc.status == TxActive {
			t.Fatalf("%s iter=%d ts=%d 事务终态不能是 active", tag, iter, ts)
		}
		if oc.rejectedOps != 0 {
			t.Fatalf("%s iter=%d ts=%d 合法操作不应被拒绝, 拒绝次数=%d", tag, iter, ts, oc.rejectedOps)
		}
	}

	refValues, refReads := serialReference(sc, outcomes)

	for k, want := range refValues {
		if got := s.Values()[k]; got != want {
			t.Fatalf("%s iter=%d 键 %s 终值不符: 实际 %d, 时间戳升序串行参照 %d", tag, iter, k, got, want)
		}
	}

	for ts, oc := range outcomes {
		if oc.status != TxCommitted {
			continue
		}
		buf := map[string]int64{}
		idx := 0
		for _, op := range sc.txs[ts] {
			if op.kind == "W" {
				buf[op.key] = op.value
				continue
			}
			want, own := buf[op.key]
			if !own {
				want = refReads[ts][op.key]
			}
			if idx >= len(oc.reads) {
				t.Fatalf("%s iter=%d ts=%d 缺少第 %d 个读记录", tag, iter, ts, idx)
			}
			if oc.reads[idx].key != op.key || oc.reads[idx].value != want {
				t.Fatalf("%s iter=%d ts=%d 读 %s 不符: 实际 %d, 串行参照 %d",
					tag, iter, ts, op.key, oc.reads[idx].value, want)
			}
			idx++
		}
		if idx != len(oc.reads) {
			t.Fatalf("%s iter=%d ts=%d 读记录数量不一致", tag, iter, ts)
		}
	}
	t.Logf("%s iter=%d 判定依据: 终态分布 %v; %d 个参照键终值与所有已提交事务逐读值均一致",
		tag, iter, statusSummary(outcomes), len(refValues))
}

// TestRandomInterleavingAgainstSerialReference 随机交错并发执行并与串行参照对拍。
func TestRandomInterleavingAgainstSerialReference(t *testing.T) {
	const iterations = 60
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(20260930 + iter)))
		sc := genScript(rng, 6+rng.Intn(8), 4, 6)

		s, outcomes := runScript(t, sc, true)
		t.Logf("输入 iter=%d: %d 个事务的随机读写脚本; 输出终态: %v", iter, len(sc.txs), statusSummary(outcomes))
		checkAgainstReference(t, "concurrent", iter, s, sc, outcomes)
	}
}

// TestReplayDeterminism 相同脚本 + 相同全局交错时刻表，多 goroutine 重放两次：
// 提交/中止集合、逐读值与每键终值必须逐字节相同（相同操作序列重放结果完全相同）。
// 另跑一组不同时刻表，验证交错不同时结果可以不同，但各自仍满足串行参照。
func TestReplayDeterminism(t *testing.T) {
	const iterations = 30
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(9810930 + iter)))
		sc := genScript(rng, 5+rng.Intn(6), 3, 5)
		schedA := mergeSchedule(rand.New(rand.NewSource(int64(7000+iter))), sc)
		schedB := mergeSchedule(rand.New(rand.NewSource(int64(8000+iter))), sc)

		s1, o1 := runScheduled(t, schedA)
		s2, o2 := runScheduled(t, schedA) // 同时刻表再放一次
		s3, o3 := runScheduled(t, schedB) // 不同时刻表

		for ts := range sc.txs {
			if o1[ts].status != o2[ts].status {
				t.Fatalf("iter=%d ts=%d 相同时刻表重放终态不一致: 第一次=%s 第二次=%s",
					iter, ts, o1[ts].status, o2[ts].status)
			}
			if len(o1[ts].reads) != len(o2[ts].reads) {
				t.Fatalf("iter=%d ts=%d 相同时刻表重放读记录数不一致", iter, ts)
			}
			for i := range o1[ts].reads {
				if o1[ts].reads[i] != o2[ts].reads[i] {
					t.Fatalf("iter=%d ts=%d 第 %d 个读重放不一致: %+v vs %+v",
						iter, ts, i, o1[ts].reads[i], o2[ts].reads[i])
				}
			}
		}
		v1, v2 := s1.Values(), s2.Values()
		for k := range unionKeys(v1, v2) {
			if v1[k] != v2[k] {
				t.Fatalf("iter=%d 键 %s 相同时刻表重放终值不一致: %d vs %d", iter, k, v1[k], v2[k])
			}
		}
		checkAgainstReference(t, "replay-A", iter, s2, sc, o2)
		checkAgainstReference(t, "replay-B", iter, s3, sc, o3)
		t.Logf("输入 iter=%d: 固定脚本 + 两个不同全局时刻表(A/B)，A 重放两次; "+
			"输出: A终态 %v, B终态 %v; 判定依据: 同时刻表重放完全一致，A/B 各自与串行参照一致",
			iter, statusSummary(o1), statusSummary(o3))
	}
}

func unionKeys(a, b map[string]int64) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}
