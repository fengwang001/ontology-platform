package scheduler_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"ontology/scheduler"
)

// txnProgram 是一个确定性的读写事务程序：
//   - 读取读集里所有键（缺省视为 0）；
func applyProgram(store map[string]int, reads, writes []string, id int, fail bool) {
	sum := 0
	for _, k := range reads {
		sum += store[k]
	}
	if fail {
		// 失败事务不写入，但完成（失败）仍算完成、仍释放调度位置。
		return
	}
	delta := id*100 + sum
	for _, k := range writes {
		store[k] += delta
	}
}

type scriptOp struct {
	kind   string // "arrive" | "complete" | "cancel"
	reads  []string
	writes []string
	id     int // complete/cancel 的目标
}

type arrival struct {
	reads  []string
	writes []string
	fail   bool
}

type diffConfig struct {
	nTxn     int
	k        int
	keySpace int
	failRate float64
}

// runParallelScript 按脚本到达事务；被放行的事务在独立 goroutine 中对真实 KV 执行，
// complete 与 cancel 也在并发 goroutine 中随机调用（只在事务运行后才尝试 complete）。
// 调度器保证：任何事务执行时，所有更早的冲突事务都已完成，因此并行结果应等价串行。
func runParallelScript(t *testing.T, rng *rand.Rand, cfg diffConfig, logBuf *bytes.Buffer) (map[string]int, []arrival, []scriptOp) {
	t.Helper()
	s := scheduler.New(cfg.k, scheduler.WithLogger(logBuf))

	var storeMu sync.Mutex
	store := make(map[string]int)
	var wg sync.WaitGroup

	script := make([]scriptOp, 0, cfg.nTxn*2)
	arrivals := make([]arrival, cfg.nTxn)

	// 每个事务一个 worker：在自己的 goroutine 中等待放行，放行后立即执行真实 KV 读写，
	// 执行落地后并发调用 Complete。等待期间若被取消（状态直接变 completed）则退出。
	startWorker := func(id int, a arrival) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !waitUntilRunnable(s, id) {
				return // 等待中被取消
			}
			storeMu.Lock()
			applyProgram(store, a.reads, a.writes, id, a.fail)
			storeMu.Unlock()
			if _, err := s.Complete(id); err != nil {
				t.Errorf("Complete(%d) unexpected error: %v", id, err)
			}
		}()
	}

	for id := 1; id <= cfg.nTxn; id++ {
		reads, writes := randomKeySets(rng, cfg.keySpace)
		fail := rng.Float64() < cfg.failRate
		arrivedID, released, err := s.Arrive(reads, writes)
		if err != nil {
			t.Fatalf("Arrive: %v (reads=%v writes=%v)", err, reads, writes)
		}
		if arrivedID != id {
			t.Fatalf("arrived id=%d want %d", arrivedID, id)
		}
		a := arrival{reads: reads, writes: writes, fail: fail}
		arrivals[id-1] = a
		script = append(script, scriptOp{kind: "arrive", reads: reads, writes: writes})
		startWorker(id, a)

		// 随机取消一个仍在等待的事务，制造“取消后阻挡解除”的并发场景。
		if rng.Float64() < 0.15 {
			candidate := 1 + rng.IntN(id)
			if st, ok := s.Status(candidate); ok && st == scheduler.StatusWaiting {
				if _, err := s.Cancel(candidate); err == nil {
					script = append(script, scriptOp{kind: "cancel", id: candidate})
				}
			}
		}
		_ = released
	}

	wg.Wait()
	return store, arrivals, script
}

// waitUntilRunnable 轮询直到事务变为 running（返回 true）或已完成即等待中被取消（false）。
func waitUntilRunnable(s *scheduler.Scheduler, id int) bool {
	for {
		st, ok := s.Status(id)
		if !ok {
			return false
		}
		if st == scheduler.StatusRunning {
			return true
		}
		if st == scheduler.StatusCompleted {
			return false
		}
		time.Sleep(50 * time.Microsecond)
	}
}

func randomKeySets(rng *rand.Rand, keySpace int) (reads, writes []string) {
	keys := func() []string {
		n := rng.IntN(3) // 0..2 个键
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, fmt.Sprintf("k%d", rng.IntN(keySpace)))
		}
		return dedup(out)
	}
	writes = keys()
	reads = keys()
	if len(reads) == 0 && len(writes) == 0 {
		writes = []string{"k0"}
	}
	return reads, writes
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, k := range in {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// TestRandomEquivalentToSerial 在多组随机种子下：
//  1. 用调度器并发执行事务程序得到 KV 终态；
//  2. 严格按到达顺序（编号升序）串行重放同一批程序（取消的事务跳过、失败事务不写入）；
//  3. 逐键比较两个终态必须完全一致。
func TestRandomEquivalentToSerial(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed*7+1)))
			cfg := diffConfig{
				nTxn:     30 + rng.IntN(40),
				k:        1 + rng.IntN(5),
				keySpace: 2 + rng.IntN(6),
				failRate: 0.1,
			}
			var logBuf bytes.Buffer
			parallelStore, arrivals, script := runParallelScript(t, rng, cfg, &logBuf)

			// 串行基线：按 id 升序执行所有成功到达且未被取消的事务。
			cancelled := map[int]bool{}
			for _, op := range script {
				if op.kind == "cancel" {
					cancelled[op.id] = true
				}
			}
			serialStore := make(map[string]int)
			for id := 1; id <= cfg.nTxn; id++ {
				if cancelled[id] {
					continue
				}
				a := arrivals[id-1]
				applyProgram(serialStore, a.reads, a.writes, id, a.fail)
			}

			if !storesEqual(parallelStore, serialStore) {
				keys := sortedKeys(parallelStore, serialStore)
				var diff strings.Builder
				for _, k := range keys {
					if parallelStore[k] != serialStore[k] {
						fmt.Fprintf(&diff, "key=%s parallel=%d serial=%d\n", k, parallelStore[k], serialStore[k])
					}
				}
				t.Fatalf("parallel result differs from arrival-order serial:\n%s\n--- script ---\n%s",
					diff.String(), renderScript(cfg, script))
			}
			t.Logf("seed=%d K=%d nTxn=%d 判定与放行日志（节选）:\n输入示例与输出见日志尾：\n%s",
				seed, cfg.k, cfg.nTxn, tailLog(&logBuf, 1500))
		})
	}
}

func storesEqual(a, b map[string]int) bool {
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

func sortedKeys(a, b map[string]int) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func renderScript(cfg diffConfig, script []scriptOp) string {
	var b strings.Builder
	fmt.Fprintf(&b, "K=%d\n", cfg.k)
	for _, op := range script {
		if op.kind == "arrive" {
			fmt.Fprintf(&b, "arrive reads=%v writes=%v\n", op.reads, op.writes)
		} else {
			fmt.Fprintf(&b, "%s id=%d\n", op.kind, op.id)
		}
	}
	return b.String()
}

func tailLog(buf *bytes.Buffer, max int) string {
	text := buf.String()
	if len(text) <= max {
		return text
	}
	return "..." + text[len(text)-max:]
}

// TestReplayDeterminism：相同操作序列重放两次，每次返回的放行序列与状态必须完全相同。
func TestReplayDeterminism(t *testing.T) {
	type step struct {
		kind        string
		id          int
		reads       []string
		writes      []string
		wantRelease []int
	}
	steps := []step{
		{kind: "arrive", writes: []string{"a"}, wantRelease: []int{1}},
		{kind: "arrive", writes: []string{"b"}, wantRelease: []int{2}},
		{kind: "arrive", reads: []string{"a"}, wantRelease: nil},
		{kind: "arrive", writes: []string{"c"}, wantRelease: nil},
		{kind: "arrive", reads: []string{"c"}, wantRelease: nil},
		{kind: "complete", id: 1, wantRelease: []int{3}},
		{kind: "cancel", id: 4, wantRelease: nil},
		{kind: "complete", id: 2, wantRelease: []int{5}}, // T3 已完成、T4 已取消，T5 此刻无更早未完成冲突者
		{kind: "complete", id: 3, wantRelease: nil},      // 已完成再次完成将被拒绝（仅记录放行结果）
	}

	run := func() []string {
		var logBuf bytes.Buffer
		s := scheduler.New(2, scheduler.WithLogger(&logBuf))
		got := make([]string, 0, len(steps))
		for i, st := range steps {
			var rel []int
			switch st.kind {
			case "arrive":
				_, rel, _ = s.Arrive(st.reads, st.writes)
			case "complete":
				rel, _ = s.Complete(st.id)
			case "cancel":
				rel, _ = s.Cancel(st.id)
			}
			if !eqInts(rel, st.wantRelease) {
				t.Fatalf("replay run step %d (%s): released=%v want %v", i, st.kind, rel, st.wantRelease)
			}
			got = append(got, fmt.Sprintf("%s:%v", st.kind, rel))
		}
		return got
	}

	first := run()
	second := run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay differs at step %d: %s vs %s", i, first[i], second[i])
		}
	}
	t.Logf("重放结果逐操作一致: %v", first)
}
