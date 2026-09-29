package scheduler_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/ontology/scheduler"
)

func mustAdd(t *testing.T, s *scheduler.Scheduler, tx scheduler.Transaction) {
	t.Helper()
	if err := s.Add(tx); err != nil {
		t.Fatalf("Add(%+v): unexpected error %v", tx, err)
	}
}

func addN(t *testing.T, txs []scheduler.Transaction) *scheduler.Scheduler {
	t.Helper()
	s := scheduler.New()
	for _, tx := range txs {
		mustAdd(t, s, tx)
	}
	return s
}

// 朴素参照：按提交顺序串行处理，嵌套循环求写集相交。
type naiveTx struct {
	seq       int
	readKeys  []string
	writeKeys []string
	values    map[string]string
}

func naiveBuild(txs []scheduler.Transaction) []naiveTx {
	out := make([]naiveTx, 0, len(txs))
	for _, tx := range txs {
		seen := map[string]bool{}
		var keys []string
		for _, k := range tx.WriteKeys {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
		out = append(out, naiveTx{
			seq:       tx.Seq,
			readKeys:  append([]string(nil), tx.ReadKeys...),
			writeKeys: keys,
			values:    tx.WriteValues,
		})
	}
	return out
}

func naiveDepths(txs []naiveTx) map[int]int {
	depth := map[int]int{}
	for i, tx := range txs {
		maxD := 0
		for j := 0; j < i; j++ {
			intersects := false
			for _, a := range tx.writeKeys {
				for _, b := range txs[j].writeKeys {
					if a == b {
						intersects = true
					}
				}
			}
			if intersects && depth[txs[j].seq] > maxD {
				maxD = depth[txs[j].seq]
			}
		}
		depth[tx.seq] = maxD + 1
	}
	return depth
}

func naiveRounds(txs []naiveTx, maxParallel int) map[int]int {
	depsOf := func(i int) []int {
		var deps []int
		for j := 0; j < i; j++ {
			for _, a := range txs[i].writeKeys {
				hit := false
				for _, b := range txs[j].writeKeys {
					if a == b {
						hit = true
					}
				}
				if hit {
					deps = append(deps, txs[j].seq)
					break
				}
			}
		}
		return deps
	}
	roundOf := map[int]int{}
	done := map[int]bool{}
	for round := 1; len(done) < len(txs); round++ {
		count := 0
		for i, tx := range txs {
			if done[tx.seq] || count >= maxParallel {
				continue
			}
			ready := true
			for _, dep := range depsOf(i) {
				if roundOf[dep] >= round {
					ready = false
				}
			}
			if ready {
				roundOf[tx.seq] = round
				done[tx.seq] = true
				count++
			}
		}
	}
	return roundOf
}

func naiveState(txs []naiveTx) map[string]string {
	state := map[string]string{}
	for _, tx := range txs {
		for _, k := range tx.writeKeys {
			if v, ok := tx.values[k]; ok {
				state[k] = v
			} else {
				state[k] = fmt.Sprintf("tx%d:%s", tx.seq, k)
			}
		}
	}
	return state
}

func TestDepthAgainstNaiveReference(t *testing.T) {
	cases := [][]scheduler.Transaction{
		{
			{Seq: 1, WriteKeys: []string{"a"}},
			{Seq: 2, WriteKeys: []string{"b"}},
			{Seq: 3, WriteKeys: []string{"c"}},
		},
		{
			{Seq: 1, WriteKeys: []string{"a", "b"}},
			{Seq: 2, WriteKeys: []string{"c"}},
			{Seq: 3, WriteKeys: []string{"b", "c"}},
			{Seq: 4, WriteKeys: []string{"d"}},
			{Seq: 5, WriteKeys: []string{"a", "d"}},
		},
		{
			{Seq: 1, WriteKeys: []string{"a"}},
			{Seq: 2, WriteKeys: []string{"a"}},
			{Seq: 3, WriteKeys: []string{"a"}},
			{Seq: 4, WriteKeys: []string{"a"}},
		},
		{
			{Seq: 1, WriteKeys: []string{"a", "b"}},
			{Seq: 2, WriteKeys: []string{"a", "c"}},
			{Seq: 3, WriteKeys: []string{"b", "c"}},
			{Seq: 4, WriteKeys: []string{"a", "b", "c"}},
		},
	}
	for ci, txs := range cases {
		s := addN(t, txs)
		ref := naiveDepths(naiveBuild(txs))
		plans, err := s.Plans(2)
		if err != nil {
			t.Fatalf("case %d: Plans: %v", ci, err)
		}
		if len(plans) != len(txs) {
			t.Fatalf("case %d: got %d plans, want %d", ci, len(plans), len(txs))
		}
		for _, p := range plans {
			if p.Depth != ref[p.Seq] {
				t.Errorf("case %d tx %d depth=%d want %d (%s)",
					ci, p.Seq, p.Depth, ref[p.Seq], p.Reason)
			}
		}
		if err := s.SelfCheck(); err != nil {
			t.Fatalf("case %d self check: %v", ci, err)
		}
	}
}

func TestWriteSetIntersectionCombinations(t *testing.T) {
	// 读集不参与依赖：tx2 读 a 但不写 a，与 tx1 无依赖。
	s := addN(t, []scheduler.Transaction{
		{Seq: 1, ReadKeys: []string{"x"}, WriteKeys: []string{"a", "b"}},
		{Seq: 2, ReadKeys: []string{"a"}, WriteKeys: []string{"c"}},
	})
	d1, _ := s.Depth(1)
	d2, _ := s.Depth(2)
	if d1 != 1 || d2 != 1 {
		t.Fatalf("read set must not create dependency: depths %d,%d", d1, d2)
	}

	// 写集内重复键只算一个。
	s2 := scheduler.New()
	mustAdd(t, s2, scheduler.Transaction{Seq: 1, WriteKeys: []string{"a", "a", "b", "b"}})
	mustAdd(t, s2, scheduler.Transaction{Seq: 2, WriteKeys: []string{"b", "c", "c"}})
	if d, _ := s2.Depth(2); d != 2 {
		t.Fatalf("dedup write set, depth=%d want 2", d)
	}

	// 只依赖每个键的最近写入者：tx3 仅依赖 tx2。
	s3 := addN(t, []scheduler.Transaction{
		{Seq: 1, WriteKeys: []string{"a"}},
		{Seq: 2, WriteKeys: []string{"a"}},
		{Seq: 3, WriteKeys: []string{"a"}},
	})
	plans, _ := s3.Plans(1)
	if got := plans[2].DependsOn; !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("tx3 dependsOn=%v want [2]", got)
	}

	if _, err := s.Depth(99); !errors.Is(err, scheduler.ErrTxNotFound) {
		t.Fatalf("Depth(99) err=%v want ErrTxNotFound", err)
	}
}

func TestScheduleRoundsParallelismBounds(t *testing.T) {
	txs := []scheduler.Transaction{
		{Seq: 1, WriteKeys: []string{"a"}},
		{Seq: 2, WriteKeys: []string{"b"}},
		{Seq: 3, WriteKeys: []string{"c"}},
		{Seq: 4, WriteKeys: []string{"a", "b"}},
		{Seq: 5, WriteKeys: []string{"c"}},
	}
	s := addN(t, txs)
	ref := naiveBuild(txs)

	for _, p := range []int{1, 2, 3, 4, 100} {
		rounds, err := s.Schedule(p)
		if err != nil {
			t.Fatalf("Schedule(%d): %v", p, err)
		}
		want := naiveRounds(ref, p)
		got := map[int]int{}
		for _, r := range rounds {
			if len(r.TxSeqs) > p {
				t.Fatalf("p=%d round %d has %d txs", p, r.Round, len(r.TxSeqs))
			}
			if r.Parallel != (len(r.TxSeqs) > 1) {
				t.Fatalf("p=%d round %d parallel flag wrong", p, r.Round)
			}
			for _, seq := range r.TxSeqs {
				got[seq] = r.Round
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("p=%d rounds=%v want %v", p, got, want)
		}
		// 同轮事务写集必须两两不相交，即不得互为依赖。
		for _, r := range rounds {
			seen := map[string]bool{}
			for _, seq := range r.TxSeqs {
				for _, k := range ref[seq-1].writeKeys {
					if seen[k] {
						t.Fatalf("p=%d round %d has dependent txs sharing key %q", p, r.Round, k)
					}
					seen[k] = true
				}
			}
		}
	}

	for _, bad := range []int{0, -1, -10} {
		if _, err := s.Schedule(bad); !errors.Is(err, scheduler.ErrInvalidParallel) {
			t.Fatalf("Schedule(%d) err=%v want ErrInvalidParallel", bad, err)
		}
	}
	if _, err := s.Plans(0); !errors.Is(err, scheduler.ErrInvalidParallel) {
		t.Fatalf("Plans(0) err=%v want ErrInvalidParallel", err)
	}
	if _, err := s.Replay(context.Background(), 0, nil); !errors.Is(err, scheduler.ErrInvalidParallel) {
		t.Fatalf("Replay(0) err=%v want ErrInvalidParallel", err)
	}
}

func TestReplayMatchesNaiveSerial(t *testing.T) {
	txs := []scheduler.Transaction{
		{Seq: 1, WriteKeys: []string{"a", "b"}, WriteValues: map[string]string{"a": "A1"}},
		{Seq: 2, WriteKeys: []string{"a", "c"}},
		{Seq: 3, WriteKeys: []string{"b", "c"}, WriteValues: map[string]string{"b": "B3", "c": "C3"}},
		{Seq: 4, WriteKeys: []string{"d"}},
		{Seq: 5, WriteKeys: []string{"a", "d"}},
	}
	want := naiveState(naiveBuild(txs))
	s := addN(t, txs)

	for _, p := range []int{1, 2, 3, 8} {
		res, err := s.Replay(context.Background(), p, nil)
		if err != nil {
			t.Fatalf("p=%d replay: %v", p, err)
		}
		if !reflect.DeepEqual(res.State, want) {
			t.Fatalf("p=%d state=%v want %v", p, res.State, want)
		}
		if len(res.Executions) != len(txs) {
			t.Fatalf("p=%d executions=%d want %d", p, len(res.Executions), len(txs))
		}
	}

	// 并发重复回放，结果逐字段一致且可复现。
	type pair struct {
		state  map[string]string
		rounds []scheduler.RoundPlan
	}
	var first pair
	var firstSet atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 3; j++ {
				res, err := s.Replay(context.Background(), 3, nil)
				if err != nil {
					t.Errorf("concurrent replay: %v", err)
					return
				}
				if !firstSet.Load() {
					first = pair{state: res.State, rounds: res.Rounds}
					firstSet.Store(true)
				} else if !reflect.DeepEqual(res.State, first.state) ||
					!reflect.DeepEqual(res.Rounds, first.rounds) {
					t.Errorf("concurrent replay mismatch")
				}
			}
		}()
	}
	wg.Wait()
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestIntraRoundExecutionIsConcurrent 用启动屏障证明同轮事务真正并发。
func TestIntraRoundExecutionIsConcurrent(t *testing.T) {
	s := addN(t, []scheduler.Transaction{
		{Seq: 1, WriteKeys: []string{"a"}},
		{Seq: 2, WriteKeys: []string{"b"}},
		{Seq: 3, WriteKeys: []string{"a", "b"}},
	})

	var mu sync.Mutex
	active := 0
	maxActive := 0
	startBarrier := make(chan struct{})
	var once sync.Once
	exec := func(tx scheduler.Transaction) (map[string]string, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		if active == 2 {
			once.Do(func() { close(startBarrier) })
		}
		mu.Unlock()

		select {
		case <-startBarrier:
		case <-time.After(2 * time.Second):
			return nil, fmt.Errorf("tx %d timed out waiting for sibling", tx.Seq)
		}
		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		active--
		mu.Unlock()
		out := make(map[string]string, len(tx.WriteKeys))
		for _, k := range tx.WriteKeys {
			out[k] = "x"
		}
		return out, nil
	}

	res, err := s.Replay(context.Background(), 2, exec)
	if err != nil {
		t.Fatal(err)
	}
	if maxActive < 2 {
		t.Fatalf("same-round txs did not run concurrently, maxActive=%d", maxActive)
	}
	if !reflect.DeepEqual(res.Rounds[0].TxSeqs, []int{1, 2}) {
		t.Fatalf("round 1 = %v want [1 2]", res.Rounds[0].TxSeqs)
	}
	if !reflect.DeepEqual(res.Rounds[1].TxSeqs, []int{3}) {
		t.Fatalf("round 2 = %v want [3]", res.Rounds[1].TxSeqs)
	}

	// maxParallel=1 时不得并发。
	mu.Lock()
	active, maxActive = 0, 0
	mu.Unlock()
	if _, err := s.Replay(context.Background(), 1, func(tx scheduler.Transaction) (map[string]string, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		out := make(map[string]string, len(tx.WriteKeys))
		for _, k := range tx.WriteKeys {
			out[k] = "z"
		}
		return out, nil
	}); err != nil {
		t.Fatal(err)
	}
	if maxActive != 1 {
		t.Fatalf("maxParallel=1 saw concurrency %d", maxActive)
	}
}

func TestReplayExecutorErrors(t *testing.T) {
	s := addN(t, []scheduler.Transaction{
		{Seq: 1, WriteKeys: []string{"a"}},
		{Seq: 2, WriteKeys: []string{"b"}},
	})
	boom := errors.New("boom")

	if _, err := s.Replay(context.Background(), 2, func(tx scheduler.Transaction) (map[string]string, error) {
		if tx.Seq == 2 {
			return nil, boom
		}
		return map[string]string{"a": "1"}, nil
	}); !errors.Is(err, boom) {
		t.Fatalf("want wrapped boom, got %v", err)
	}

	// 执行体写了未声明的键 -> 整体失败。
	if _, err := s.Replay(context.Background(), 1, func(tx scheduler.Transaction) (map[string]string, error) {
		out := map[string]string{}
		for _, k := range tx.WriteKeys {
			out[k] = "v"
		}
		out["rogue"] = "v"
		return out, nil
	}); !errors.Is(err, scheduler.ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument for rogue write, got %v", err)
	}
}

func TestInvalidInputsRejectedDistinctReasons(t *testing.T) {
	base := []scheduler.Transaction{
		{Seq: 1, WriteKeys: []string{"a"}},
		{Seq: 2, WriteKeys: []string{"b"}},
	}
	cases := []struct {
		name string
		tx   scheduler.Transaction
		want error
	}{
		{"non positive seq", scheduler.Transaction{Seq: 0, WriteKeys: []string{"a"}}, scheduler.ErrInvalidArgument},
		{"gap", scheduler.Transaction{Seq: 4, WriteKeys: []string{"a"}}, scheduler.ErrSeqNotConsecutive},
		{"duplicate seq", scheduler.Transaction{Seq: 2, WriteKeys: []string{"a"}}, scheduler.ErrSeqNotConsecutive},
		{"empty write set", scheduler.Transaction{Seq: 3, WriteKeys: nil}, scheduler.ErrWriteSetEmpty},
		{"empty key in writes", scheduler.Transaction{Seq: 3, WriteKeys: []string{"a", ""}}, scheduler.ErrWriteSetEmptyKey},
		{"empty read key", scheduler.Transaction{Seq: 3, ReadKeys: []string{""}, WriteKeys: []string{"a"}}, scheduler.ErrInvalidArgument},
		{
			"write value for non-write key",
			scheduler.Transaction{Seq: 3, WriteKeys: []string{"a"}, WriteValues: map[string]string{"b": "v"}},
			scheduler.ErrInvalidArgument,
		},
	}
	seen := map[error]string{}
	for _, tc := range cases {
		s := addN(t, base)
		err := s.Add(tc.tx)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err=%v want %v", tc.name, err, tc.want)
		}
		if err == nil || err.Error() == "" {
			t.Errorf("%s: error must carry a distinguishable message", tc.name)
		}
		seen[tc.want] = tc.name
	}
	// 各错误类别互不相同。
	distinct := []error{
		scheduler.ErrInvalidArgument,
		scheduler.ErrSeqNotConsecutive,
		scheduler.ErrWriteSetEmpty,
		scheduler.ErrWriteSetEmptyKey,
		scheduler.ErrTxLimitExceeded,
		scheduler.ErrTxNotFound,
		scheduler.ErrInvalidParallel,
	}
	for i := 0; i < len(distinct); i++ {
		for j := i + 1; j < len(distinct); j++ {
			if errors.Is(distinct[i], distinct[j]) {
				t.Fatalf("error categories %v and %v not distinguishable", distinct[i], distinct[j])
			}
		}
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	s := scheduler.New()
	mustAdd(t, s, scheduler.Transaction{Seq: 1, WriteKeys: []string{"a", "a"}})

	bad := []scheduler.Transaction{
		{Seq: 3, WriteKeys: []string{"b"}},                         // 跳号
		{Seq: 2, WriteKeys: nil},                                   // 写集空
		{Seq: 2, WriteKeys: []string{""}},                          // 空键
		{Seq: 2, ReadKeys: []string{""}, WriteKeys: []string{"b"}}, // 读空键
		{Seq: -1, WriteKeys: []string{"b"}},                        // 非法序号
	}
	for _, tx := range bad {
		if err := s.Add(tx); err == nil {
			t.Fatalf("bad tx %+v should be rejected", tx)
		}
	}

	if s.Len() != 1 {
		t.Fatalf("Len=%d want 1 after rejections", s.Len())
	}
	if d, _ := s.Depth(1); d != 1 {
		t.Fatalf("depth of tx1 changed to %d", d)
	}
	// 被拒后紧接着的合法序号 2 必须照常被接受，且状态完全等同从未拒过。
	mustAdd(t, s, scheduler.Transaction{Seq: 2, WriteKeys: []string{"a"}})
	if d, _ := s.Depth(2); d != 2 {
		t.Fatalf("tx2 depth=%d want 2", d)
	}
	plans, _ := s.Plans(1)
	if len(plans) != 2 || plans[1].Round != 2 {
		t.Fatalf("plans after rejection+accept: %+v", plans)
	}
	res, err := s.Replay(context.Background(), 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.State["a"] != "tx2:a" {
		t.Fatalf("state after rejections = %v", res.State)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionLimit(t *testing.T) {
	s := scheduler.New()
	mustAdd(t, s, scheduler.Transaction{Seq: 1, WriteKeys: []string{"k"}})
	if testing.Short() {
		t.Skip("limit test skipped in -short")
	}

	for i := 2; i <= scheduler.MaxTransactions; i++ {
		mustAdd(t, s, scheduler.Transaction{Seq: i, WriteKeys: []string{"k"}})
	}

	before := s.Len()
	err := s.Add(scheduler.Transaction{
		Seq:       scheduler.MaxTransactions + 1,
		WriteKeys: []string{"k"},
	})
	if !errors.Is(err, scheduler.ErrTxLimitExceeded) {
		t.Fatalf("err=%v want ErrTxLimitExceeded", err)
	}
	if s.Len() != before {
		t.Fatal("rejected over-limit Add changed scheduler state")
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentQueriesAreStable(t *testing.T) {
	txs := []scheduler.Transaction{
		{Seq: 1, ReadKeys: []string{"q"}, WriteKeys: []string{"a", "b"}},
		{Seq: 2, ReadKeys: []string{"a"}, WriteKeys: []string{"c"}},
		{Seq: 3, WriteKeys: []string{"b", "c"}},
		{Seq: 4, WriteKeys: []string{"a"}},
	}
	s := addN(t, txs)
	wantPlans, _ := s.Plans(3)
	wantRounds, _ := s.Schedule(3)

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				plans, err := s.Plans(3)
				if err != nil || !reflect.DeepEqual(plans, wantPlans) {
					t.Errorf("Plans mismatch: %v", err)
					return
				}
				rounds, err := s.Schedule(3)
				if err != nil || !reflect.DeepEqual(rounds, wantRounds) {
					t.Errorf("Schedule mismatch: %v", err)
					return
				}
				d, err := s.Depth(3)
				if err != nil || d != wantPlans[2].Depth {
					t.Errorf("Depth mismatch: %d %v", d, err)
					return
				}
				res, err := s.Replay(context.Background(), 3, nil)
				if err != nil {
					t.Errorf("Replay: %v", err)
					return
				}
				if got := naiveState(naiveBuild(txs)); !reflect.DeepEqual(res.State, got) {
					t.Errorf("Replay state mismatch")
					return
				}
				if err := s.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
					return
				}
				var buf bytes.Buffer
				if err := s.DumpAuditLog(&buf, 3); err != nil {
					t.Errorf("audit: %v", err)
					return
				}
				log := buf.String()
				for _, tx := range txs {
					needle := fmt.Sprintf("tx %d:", tx.Seq)
					if !strings.Contains(log, needle) {
						t.Errorf("audit log missing %s", needle)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

func TestAuditLogContent(t *testing.T) {
	s := addN(t, []scheduler.Transaction{
		{Seq: 1, ReadKeys: []string{"r1"}, WriteKeys: []string{"a", "b"}},
		{Seq: 2, ReadKeys: []string{"a"}, WriteKeys: []string{"a"}},
	})
	var buf bytes.Buffer
	if err := s.DumpAuditLog(&buf, 1); err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{
		"maxParallel=1",
		"tx 1:", "readKeys=[r1]", "writeKeys=[a,b]", "depth=1", "round=1",
		`reason="write set disjoint`,
		"tx 2:", "readKeys=[a]", "writeKeys=[a]", "depth=2", "round=2",
		"dependsOn=[1]",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("audit log missing %q:\n%s", want, log)
		}
	}
	if err := s.DumpAuditLog(&bytes.Buffer{}, 0); !errors.Is(err, scheduler.ErrInvalidParallel) {
		t.Fatalf("audit with bad parallelism: %v", err)
	}
}

func TestCallerSlicesNotRetained(t *testing.T) {
	s := scheduler.New()
	keys := []string{"a", "b"}
	mustAdd(t, s, scheduler.Transaction{Seq: 1, WriteKeys: keys})
	keys[0] = "MUTATED"
	d, _ := s.Depth(1)
	if d != 1 {
		t.Fatalf("depth changed after caller mutation: %d", d)
	}
	res, err := s.Replay(context.Background(), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.State["a"]; !ok {
		t.Fatalf("internal write set aliased caller slice: %v", res.State)
	}
}
