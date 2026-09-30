package scheduler

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"
)

type opKind int

const (
	opRead opKind = iota
	opWrite
	opCommit
)

type planOp struct {
	kind  opKind
	key   string
	value int64
}

type opResult struct {
	readValue int64
	committed bool
	abort     AbortReason // 中止原因；非中止为空
	rejected  string      // 被拒绝的错误；合法中止不算拒绝
}

type runResult struct {
	txCount int
	plans   [][]planOp
	results [][]opResult
}

// generatePlan 用固定随机种子生成每个事务各自的操作序列。
// 所有事务先串行 BEGIN（时间戳即其序号），再执行各自序列。
func generatePlan(rng *rand.Rand, txnCount, keyCount, maxOps int) [][]planOp {
	plans := make([][]planOp, txnCount)
	for i := range plans {
		n := rng.Intn(maxOps + 1)
		ops := make([]planOp, 0, n+1)
		for j := 0; j < n; j++ {
			key := fmt.Sprintf("k%d", rng.Intn(keyCount))
			if rng.Intn(2) == 0 {
				ops = append(ops, planOp{kind: opRead, key: key})
			} else {
				ops = append(ops, planOp{kind: opWrite, key: key,
					value: rng.Int63n(1000) + 1})
			}
		}
		ops = append(ops, planOp{kind: opCommit})
		plans[i] = ops
	}
	return plans
}

func execute(s *Scheduler, plans [][]planOp, concurrent bool) *runResult {
	// 先领取时间戳：第 i 个事务时间戳为 i+1。
	timestamps := make([]int64, len(plans))
	for i := range plans {
		timestamps[i] = s.Begin()
	}

	results := make([][]opResult, len(plans))
	runOne := func(i int) {
		ts := timestamps[i]
		rs := make([]opResult, 0, len(plans[i]))
		for _, op := range plans[i] {
			if concurrent {
				time.Sleep(time.Microsecond)
			}
			switch op.kind {
			case opRead:
				v, err := s.Read(ts, op.key)
				rs = append(rs, classifyRead(v, err))
			case opWrite:
				err := s.Write(ts, op.key, op.value)
				rs = append(rs, classifyTerminal(err))
			case opCommit:
				// 已中止事务的提交会被拒绝，序列到此结束。
				if len(rs) > 0 && rs[len(rs)-1].abort != "" {
					rs = append(rs, classifyTerminal(s.Commit(ts)))
					break
				}
				rs = append(rs, classifyTerminal(s.Commit(ts)))
			}
		}
		results[i] = rs
	}

	if concurrent {
		var wg sync.WaitGroup
		wg.Add(len(plans))
		for i := range plans {
			go func(i int) { defer wg.Done(); runOne(i) }(i)
		}
		wg.Wait()
	} else {
		for i := range plans {
			runOne(i)
		}
	}
	return &runResult{txCount: len(plans), plans: plans, results: results}
}

func classifyRead(v int64, err error) opResult {
	var ae *AbortError
	if errors.As(err, &ae) {
		return opResult{abort: ae.Reason}
	}
	if err != nil {
		return opResult{rejected: err.Error()}
	}
	return opResult{readValue: v}
}

func classifyTerminal(err error) opResult {
	var ae *AbortError
	if errors.As(err, &ae) {
		return opResult{abort: ae.Reason}
	}
	if err != nil {
		return opResult{rejected: err.Error()}
	}
	return opResult{committed: true}
}

// verifyAgainstSerialReference 按「时间戳升序串行执行已提交事务、被忽略的
// 写视同被后者覆盖」构建参照，并核对：
//  1. 每个已提交事务每次读的值 = 参照值（自有缓冲优先，否则取更早的
//     已提交事务对该键的最后一次写，没有则为 0）；
//  2. 最终每个键的值与写时间戳 = 时间戳最大的已提交写者。
func verifyAgainstSerialReference(t *testing.T, s *Scheduler, r *runResult, tag string) {
	t.Helper()

	committed := map[int]bool{}
	for i := 0; i < r.txCount; i++ {
		rs := r.results[i]
		if len(rs) > 0 && rs[len(rs)-1].committed {
			committed[i+1] = true
		}
	}

	tsList := make([]int, 0, len(committed))
	for ts := range committed {
		tsList = append(tsList, ts)
	}
	sort.Ints(tsList)

	// 参照值：按时间戳升序重放已提交事务，后者直接覆盖前者。
	refValue := map[string]int64{}
	for _, ts := range tsList {
		i := ts - 1
		ownBuffer := map[string]int64{}
		for j, op := range r.plans[i] {
			res := r.results[i][j]
			switch op.kind {
			case opRead:
				want, fromBuffer := ownBuffer[op.key]
				if !fromBuffer {
					want = refValue[op.key] // map 缺省即初值 0
				}
				if res.abort != "" || res.rejected != "" {
					t.Fatalf("[%s] 已提交事务 ts=%d 的读不应中止或被拒绝: %+v",
						tag, ts, res)
				}
				if res.readValue != want {
					t.Fatalf("[%s] 已提交事务 ts=%d 读 %q 得到 %d，串行参照期望 %d（buffer=%v）",
						tag, ts, op.key, res.readValue, want, fromBuffer)
				}
			case opWrite:
				ownBuffer[op.key] = op.value
			}
		}
		// 该事务的全部缓冲写按时间戳顺序覆盖参照值。
		keys := make([]string, 0, len(ownBuffer))
		for k := range ownBuffer {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			refValue[k] = ownBuffer[k]
		}
	}

	// 最终键值与写时间戳核对。
	refWTS := map[string]int64{}
	for _, ts := range tsList {
		written := map[string]bool{}
		for _, op := range r.plans[ts-1] {
			if op.kind == opWrite {
				written[op.key] = true
			}
		}
		for k := range written {
			if int64(ts) > refWTS[k] {
				refWTS[k] = int64(ts)
			}
		}
	}
	for k, want := range refValue {
		got := s.Inspect(k)
		if got.Value != want || got.WriteTS != refWTS[k] {
			t.Fatalf("[%s] 最终键 %q 得到 %+v，参照期望 value=%d WTS=%d",
				tag, k, got, want, refWTS[k])
		}
	}
	t.Logf("[%s] 判定: %d 个已提交事务的全部读值与最终键状态均与时间戳升序串行参照一致",
		tag, len(committed))
}

func logPlan(t *testing.T, tag string, plans [][]planOp, r *runResult) {
	t.Helper()
	for i, ops := range plans {
		ts := i + 1
		for j, op := range ops {
			res := r.results[i][j]
			switch op.kind {
			case opRead:
				if res.abort != "" {
					t.Logf("[%s] 输入: READ ts=%d key=%q -> 输出: 中止 %s（判定依据: ts < WTS）",
						tag, ts, op.key, res.abort)
				} else if res.rejected != "" {
					t.Logf("[%s] 输入: READ ts=%d key=%q -> 输出: 拒绝 %s",
						tag, ts, op.key, res.rejected)
				} else {
					t.Logf("[%s] 输入: READ ts=%d key=%q -> 输出: %d",
						tag, ts, op.key, res.readValue)
				}
			case opWrite:
				t.Logf("[%s] 输入: WRITE ts=%d key=%q value=%d -> 输出: %s%s",
					tag, ts, op.key, op.value, res.abort, res.rejected)
			case opCommit:
				switch {
				case res.committed:
					t.Logf("[%s] 输入: COMMIT ts=%d -> 输出: 已提交", tag, ts)
				case res.abort != "":
					t.Logf("[%s] 输入: COMMIT ts=%d -> 输出: 中止 %s（判定依据: 复查 ts < RTS）",
						tag, ts, res.abort)
				default:
					t.Logf("[%s] 输入: COMMIT ts=%d -> 输出: 拒绝 %s", tag, ts, res.rejected)
				}
			}
		}
	}
}

func resultsEqual(a, b *runResult) bool {
	if a.txCount != b.txCount {
		return false
	}
	for i := range a.results {
		if len(a.results[i]) != len(b.results[i]) {
			return false
		}
		for j := range a.results[i] {
			if a.results[i][j] != b.results[i][j] {
				return false
			}
		}
	}
	return true
}

// TestRandomInterleavingMatchesSerialReference 随机交错执行与串行参照对拍，
// 并验证相同操作序列重放结果完全相同。
func TestRandomInterleavingMatchesSerialReference(t *testing.T) {
	const (
		txnCount   = 12
		keyCount   = 4
		maxOps     = 8
		iterations = 30
	)

	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(42 + iter)))
		plans := generatePlan(rng, txnCount, keyCount, maxOps)

		// 串行执行两次：重放结果必须完全相同。
		s1 := NewWithLogger(io.Discard)
		r1 := execute(s1, plans, false)
		s2 := NewWithLogger(io.Discard)
		r2 := execute(s2, plans, false)
		if !resultsEqual(r1, r2) {
			t.Fatalf("第 %d 轮：相同操作序列重放结果不一致", iter)
		}

		// 并发随机交错执行。
		sc := NewWithLogger(io.Discard)
		rc := execute(sc, plans, true)

		// 两种执行的已提交集合可能因调度不同而不同（中止是合法结果），
		// 但各自都必须满足与时间戳升序串行参照的完全一致。
		verifyAgainstSerialReference(t, s1, r1, fmt.Sprintf("串行#%d", iter))
		verifyAgainstSerialReference(t, sc, rc, fmt.Sprintf("并发#%d", iter))

		if iter == 0 {
			logPlan(t, "并发样例", plans, rc)
			t.Logf("[并发样例] 判定: 输入/输出如上，键状态与串行参照核对通过")
		}
	}
}
