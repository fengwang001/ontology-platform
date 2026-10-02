package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func names(ks ...int64) []TxnName {
	out := make([]TxnName, 0, len(ks))
	for _, k := range ks {
		out = append(out, TxnName{S: 0, K: k})
	}
	return out
}

func names2(pairs ...int) []TxnName {
	out := make([]TxnName, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, TxnName{S: pairs[i], K: int64(pairs[i+1])})
	}
	return out
}

func mustWrite(t *testing.T, r *Registry, s int, n int64) {
	t.Helper()
	if err := r.Write(s, n); err != nil {
		t.Fatalf("Write(%d,%d): %v", s, n, err)
	}
}

func checkConservation(t *testing.T, r *Registry, total int64) {
	t.Helper()
	st := r.Stats()
	if got := st.Committed + st.Aborted + st.Open + st.Prepared; got != total {
		t.Fatalf("record conservation violated: %d != total %d (stats %+v)", got, total, st)
	}
}

// 题目给出的第一个完整示例。
func TestSpecExampleRestore(t *testing.T) {
	r, err := NewRegistry(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, r, 0, 3)
	mustWrite(t, r, 1, 2)
	if got, err := r.Barrier(1); err != nil || fmt.Sprint(got) != fmt.Sprint(names2(0, 1, 1, 1)) {
		t.Fatalf("Barrier(1)=%v,%v", got, err)
	}
	mustWrite(t, r, 0, 4)
	if got, err := r.Barrier(2); err != nil || fmt.Sprint(got) != fmt.Sprint(names(2)) {
		t.Fatalf("Barrier(2)=%v,%v", got, err)
	}
	if got, err := r.Complete(1); err != nil || fmt.Sprint(got) != fmt.Sprint(names2(0, 1, 1, 1)) {
		t.Fatalf("Complete(1)=%v,%v", got, err)
	}
	mustWrite(t, r, 0, 1)
	mustWrite(t, r, 1, 7)
	if got, err := r.Barrier(3); err != nil || fmt.Sprint(got) != fmt.Sprint(names2(0, 3, 1, 3)) {
		t.Fatalf("Barrier(3)=%v,%v", got, err)
	}
	mustWrite(t, r, 1, 6) // 名 (1,4)

	res, err := r.Restore(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res.Committed) != fmt.Sprint(names(2)) {
		t.Fatalf("restore committed=%v", res.Committed)
	}
	wantAborted := names2(0, 3, 1, 3, 1, 4)
	if fmt.Sprint(res.Aborted) != fmt.Sprint(wantAborted) {
		t.Fatalf("restore aborted=%v want %v", res.Aborted, wantAborted)
	}
	if res.Probes != 7 {
		t.Fatalf("restore probes=%d want 7", res.Probes)
	}
	st := r.Stats()
	if st.Committed != 9 || st.Aborted != 14 || st.ProbeCount != 7 {
		t.Fatalf("stats after restore=%+v", st)
	}
	info, err := r.Txn(0, 2)
	if err != nil || info.State != COMMITTED || info.N != 4 {
		t.Fatalf("Txn(0,2)=%+v,%v", info, err)
	}

	// 恢复后 lb=ln=2，life=1；Write(0,5) 名 (0,3) 下是 ABORTED，世代加一。
	mustWrite(t, r, 0, 5)
	info, err = r.Txn(0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != OPEN || info.Epoch != 1 || info.N != 5 {
		t.Fatalf("Txn(0,3) after write=%+v want OPEN epoch1 n5", info)
	}
	checkConservation(t, r, 28)
}

// 题目给出的第二个示例：M=2，缺失耗尽后更远处的事务遗留，
// 之后推进到同名时由 Write 先中止再重开。
func TestSpecExampleLingeringOpen(t *testing.T) {
	r, _ := NewRegistry(1, 2)
	mustWrite(t, r, 0, 1)
	r.Barrier(1)
	r.Barrier(2)
	r.Barrier(3)
	mustWrite(t, r, 0, 5) // 名 (0,4)

	res, err := r.Restore(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Committed) != 0 {
		t.Fatalf("committed=%v", res.Committed)
	}
	if fmt.Sprint(res.Aborted) != fmt.Sprint(names(1)) {
		t.Fatalf("aborted=%v", res.Aborted)
	}
	if res.Probes != 3 {
		t.Fatalf("probes=%d want 3", res.Probes)
	}
	info, _ := r.Txn(0, 4)
	if info.State != OPEN || info.N != 5 {
		t.Fatalf("(0,4) should linger OPEN, got %+v", info)
	}

	r.Barrier(1)
	r.Barrier(2)
	r.Barrier(3)
	mustWrite(t, r, 0, 2) // 名 (0,4)：遗留 OPEN 先中止
	info, _ = r.Txn(0, 4)
	if info.State != OPEN || info.Epoch != 1 || info.N != 2 {
		t.Fatalf("(0,4) reopened=%+v want OPEN epoch1 n2", info)
	}
	st := r.Stats()
	if st.Aborted != 6 { // 1 + 5
		t.Fatalf("aborted=%d want 6", st.Aborted)
	}
	checkConservation(t, r, 8)
}

// 事务名恒为 (s, lb+1)，Barrier 后换名；没有写入的子任务不产生事务。
func TestNamingAndEmptySubtasks(t *testing.T) {
	r, _ := NewRegistry(3, 1)
	mustWrite(t, r, 2, 10)
	if got := r.Pending(); fmt.Sprint(got) != fmt.Sprint(names2(2, 1)) {
		t.Fatalf("pending=%v", got)
	}
	got, err := r.Barrier(1)
	if err != nil || len(got) != 1 || got[0] != (TxnName{2, 1}) {
		t.Fatalf("Barrier=%v,%v want only (2,1)", got, err)
	}
	mustWrite(t, r, 0, 1)
	mustWrite(t, r, 2, 2) // 新名 (2,2)
	got, _ = r.Barrier(2)
	if fmt.Sprint(got) != fmt.Sprint(names2(0, 2, 2, 2)) {
		t.Fatalf("Barrier(2)=%v", got)
	}
	if _, err := r.Txn(1, 1); !errors.Is(err, ErrNoSuchTxn) {
		t.Fatalf("Txn(1,1) err=%v", err)
	}
	mustWrite(t, r, 2, 3)
	info, _ := r.Txn(2, 3)
	if info.State != OPEN || info.N != 3 {
		t.Fatalf("Txn(2,3)=%+v", info)
	}
}

// Complete 跳号：被跳过检查点的事务由后来的通知一并提交，顺序 (k,s)。
func TestCompleteSkips(t *testing.T) {
	r, _ := NewRegistry(2, 1)
	mustWrite(t, r, 0, 1)
	mustWrite(t, r, 1, 1)
	r.Barrier(1)
	mustWrite(t, r, 0, 1)
	r.Barrier(2)
	mustWrite(t, r, 1, 1)
	r.Barrier(3)
	got, err := r.Complete(3)
	if err != nil {
		t.Fatal(err)
	}
	want := names2(0, 1, 1, 1, 0, 2, 1, 3)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Complete(3)=%v want %v", got, want)
	}
	st := r.Stats()
	if st.Committed != 4 || st.Open != 0 || st.Prepared != 0 {
		t.Fatalf("stats=%+v", st)
	}
}

// Complete 通知过期与超前（互斥，先报过期）；参数非法最先。
func TestCompleteStaleAndAhead(t *testing.T) {
	r, _ := NewRegistry(1, 1)
	if _, err := r.Complete(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Complete(0)=%v", err)
	}
	mustWrite(t, r, 0, 1)
	r.Barrier(1)
	r.Complete(1)
	if _, err := r.Complete(1); !errors.Is(err, ErrStaleNotice) {
		t.Fatalf("Complete(1) again=%v", err)
	}
	if _, err := r.Complete(2); !errors.Is(err, ErrFutureNotice) {
		t.Fatalf("Complete(2)=%v", err)
	}
}

// Barrier 检查点乱序与非法参数。
func TestBarrierOrdering(t *testing.T) {
	r, _ := NewRegistry(1, 1)
	if _, err := r.Barrier(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Barrier(0)=%v", err)
	}
	r.Barrier(1)
	if _, err := r.Barrier(1); !errors.Is(err, ErrStaleBarrier) {
		t.Fatalf("Barrier(1)=%v", err)
	}
	if _, err := r.Barrier(3); !errors.Is(err, ErrStaleBarrier) {
		t.Fatalf("Barrier(3)=%v", err)
	}
}

// 构造参数与 Write 参数非法。
func TestInvalidArguments(t *testing.T) {
	for _, pm := range [][2]int{{0, 1}, {65, 1}, {1, 0}, {1, 1001}, {-1, 1}, {1, -1}} {
		if _, err := NewRegistry(pm[0], pm[1]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("NewRegistry(%v)=%v", pm, err)
		}
	}
	r, _ := NewRegistry(2, 1)
	bad := []struct {
		s int
		n int64
	}{
		{-1, 1}, {2, 1}, {0, 0}, {0, 1_000_001},
	}
	for _, b := range bad {
		if err := r.Write(b.s, b.n); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Write(%d,%d)=%v", b.s, b.n, err)
		}
	}
	if _, err := r.Restore(-1, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Restore(-1)=%v", err)
	}
	if _, err := r.Restore(0, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Restore(0,0)=%v", err)
	}
	if _, err := r.Restore(0, 65); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Restore(0,65)=%v", err)
	}
}

// Restore 先提交 (ln,c] 内事务再清扫；清扫从 c+1 起不碰 c 及以前；
// 缺失计数在命中后清零，恰好 M 个连续缺失结束，终态名字也算缺失。
func TestRestoreCommitThenSweep(t *testing.T) {
	for _, M := range []int{1, 2} {
		t.Run(fmt.Sprintf("M%d", M), func(t *testing.T) {
			r, _ := NewRegistry(1, M)
			for k := int64(1); k <= 6; k++ {
				mustWrite(t, r, 0, k)
				r.Barrier(k)
			}
			r.Complete(1)
			// life0 的 Restore(2)：提交 k=2；清扫 3..6 连续命中，随后 M 个缺失结束。
			res, err := r.Restore(2, 1)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(res.Committed) != fmt.Sprint(names(2)) {
				t.Fatalf("committed=%v", res.Committed)
			}
			if fmt.Sprint(res.Aborted) != fmt.Sprint(names(3, 4, 5, 6)) {
				t.Fatalf("aborted=%v", res.Aborted)
			}
			// 探测自 k=3 起：3,4,5,6 命中后再 M 个缺失结束。
			if res.Probes != int64(M)+4 {
				t.Fatalf("M=%d first probes=%d want %d", M, res.Probes, M+4)
			}
			// life1：让 (0,5) 成为悬挂 PREPARED，(0,6) 是旧 ABORTED（算缺失）。
			r.Barrier(3)
			r.Barrier(4)
			mustWrite(t, r, 0, 5)
			r.Barrier(5)
			res2, err := r.Restore(4, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(res2.Committed) != 0 {
				t.Fatalf("life1 restore committed=%v", res2.Committed)
			}
			if fmt.Sprint(res2.Aborted) != fmt.Sprint(names(5)) {
				t.Fatalf("life1 aborted=%v", res2.Aborted)
			}
			// 5 命中清零；6 终态算缺失；随后还需 M-1 个真缺失。
			if res2.Probes != int64(M)+1 {
				t.Fatalf("M=%d probes=%d want %d", M, res2.Probes, M+1)
			}
			for k := int64(1); k <= 4; k++ {
				info, _ := r.Txn(0, k)
				if k <= 2 && info.State != COMMITTED {
					t.Fatalf("(0,%d)=%v want COMMITTED", k, info.State)
				}
				if k >= 3 && info.State != ABORTED {
					t.Fatalf("(0,%d)=%v want ABORTED", k, info.State)
				}
			}
		})
	}
}

// M=1 时 c+2 处的悬挂事务遗留不被清扫。
func TestLingeringWhenMOne(t *testing.T) {
	r, _ := NewRegistry(1, 1)
	r.Barrier(1)          // 无写入：(0,1) 不存在，Restore 探测它算缺失
	mustWrite(t, r, 0, 8) // 名 (0,2)
	r.Barrier(2)
	res, err := r.Restore(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Probes != 1 || len(res.Aborted) != 0 {
		t.Fatalf("probes=%d aborted=%v", res.Probes, res.Aborted)
	}
	info2, _ := r.Txn(0, 2)
	if info2.State != PREPARED || info2.N != 8 {
		t.Fatalf("(0,2)=%+v want PREPARED 8 (linger beyond window)", info2)
	}
}

// 缩容 s 取到旧并行度；扩容取到新并行度。
func TestRestoreResize(t *testing.T) {
	r, _ := NewRegistry(3, 1)
	for s := 0; s < 3; s++ {
		mustWrite(t, r, s, int64(s+1))
	}
	res, err := r.Restore(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	// M=1：每个 s 先在 k=1 命中（计数清零），再在 k=2 缺失一次后结束，共 6 次。
	if fmt.Sprint(res.Aborted) != fmt.Sprint(names2(0, 1, 1, 1, 2, 1)) {
		t.Fatalf("shrink aborted=%v", res.Aborted)
	}
	if res.Probes != 6 {
		t.Fatalf("shrink probes=%d want 6", res.Probes)
	}
	if err := r.Write(2, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Write after shrink=%v", err)
	}

	r2, _ := NewRegistry(1, 1)
	mustWrite(t, r2, 0, 1)
	res2, err := r2.Restore(0, 3)
	if err != nil {
		t.Fatal(err)
	}
	// M=1：s=0 在 k=1 命中清零、k=2 缺失结束；s=1、s=2 各缺失一次。
	if len(res2.Aborted) != 1 || res2.Probes != 4 {
		t.Fatalf("grow aborted=%v probes=%d", res2.Aborted, res2.Probes)
	}
	mustWrite(t, r2, 2, 1) // 扩容后 s=2 可写
}

// 遗留 PREPARED 不被后来的 Complete 提交，并可被后来的 Write 先中止再重开。
func TestStalePreparedNeverCommitted(t *testing.T) {
	r, _ := NewRegistry(1, 1)
	r.Barrier(1)          // k=1 无写入
	r.Barrier(2)          // k=2 也无写入，给清扫留出缺口
	mustWrite(t, r, 0, 3) // 名 (0,3)
	r.Barrier(3)
	r.Restore(1, 1) // M=1：清扫从 k=2 起，首个即缺失并结束，(0,3) 遗留 PREPARED
	// lb=ln=1：先做一次空通知 Complete(1)（过期边界由其他用例覆盖），
	// 再推进到 Barrier(2) 后 Complete(2) —— 旧 life 的 (0,3) 不得被提交。
	r.Barrier(2) // k=2 仍无写入
	got, err := r.Complete(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("stale prepared must not be committed: %v", got)
	}
	info, _ := r.Txn(0, 3)
	if info.State != PREPARED || info.N != 3 {
		t.Fatalf("(0,3)=%+v want stale PREPARED", info)
	}
	mustWrite(t, r, 0, 4) // 旧 life PREPARED 先中止再重开
	info, _ = r.Txn(0, 3)
	if info.State != OPEN || info.Epoch != 1 || info.N != 4 {
		t.Fatalf("(0,2) reopened=%+v want OPEN epoch1 n4", info)
	}
	if st := r.Stats(); st.Aborted != 3 {
		t.Fatalf("aborted=%d want 3", st.Aborted)
	}
	checkConservation(t, r, 7)
}

// Restore 回退/超前互斥，参数非法优先。
func TestRestoreBounds(t *testing.T) {
	r, _ := NewRegistry(1, 1)
	mustWrite(t, r, 0, 1)
	r.Barrier(1)
	r.Complete(1)
	r.Barrier(2)
	if _, err := r.Restore(0, 1); !errors.Is(err, ErrRestoreFallback) {
		t.Fatalf("restore fallback=%v", err)
	}
	if _, err := r.Restore(3, 1); !errors.Is(err, ErrRestoreAhead) {
		t.Fatalf("restore ahead=%v", err)
	}
	if _, err := r.Restore(-1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid must win: %v", err)
	}
}

// 被拒绝的操作不改变事务表、lb、ln、life、P 与任何计数。
func TestRejectedOpsDoNotMutate(t *testing.T) {
	r, _ := NewRegistry(2, 2)
	mustWrite(t, r, 0, 3)
	r.Barrier(1)
	mustWrite(t, r, 1, 2)
	r.Complete(1)

	snapshot := func() string {
		return fmt.Sprintf("p=%d lb=%d ln=%d life=%d pending=%v stats=%+v",
			r.p, r.lb, r.ln, r.life, r.Pending(), r.Stats())
	}
	before := snapshot()

	if err := r.Write(5, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Write(5)=%v", err)
	}
	if err := r.Write(0, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Write(0,0)=%v", err)
	}
	if _, err := r.Barrier(3); !errors.Is(err, ErrStaleBarrier) {
		t.Fatalf("Barrier(3)=%v", err)
	}
	if _, err := r.Complete(1); !errors.Is(err, ErrStaleNotice) {
		t.Fatalf("Complete(1)=%v", err)
	}
	if _, err := r.Complete(5); !errors.Is(err, ErrFutureNotice) {
		t.Fatalf("Complete(5)=%v", err)
	}
	if _, err := r.Restore(0, 1); !errors.Is(err, ErrRestoreFallback) {
		t.Fatalf("Restore(0)=%v", err)
	}
	if _, err := r.Restore(2, 1); !errors.Is(err, ErrRestoreAhead) {
		t.Fatalf("Restore(2)=%v", err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("state changed by rejected ops:\nbefore %s\nafter  %s", before, after)
	}
}

// visited 两档对照：已有 10 个与 100000 个已提交检查点时，
// 各做一次只提交一个事务的 Complete，visited 增量必须相等，
// 且不超过本次提交事务数加一（即为 1）。
func TestVisitedScaleIndependent(t *testing.T) {
	measure := func(prior int64) int64 {
		r, _ := NewRegistry(1, 1)
		for k := int64(1); k <= prior; k++ {
			mustWrite(t, r, 0, 1)
			if _, err := r.Barrier(k); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Complete(k); err != nil {
				t.Fatal(err)
			}
		}
		mustWrite(t, r, 0, 1)
		if _, err := r.Barrier(prior + 1); err != nil {
			t.Fatal(err)
		}
		v0 := r.Stats().Visited
		got, err := r.Complete(prior + 1)
		if err != nil || len(got) != 1 {
			t.Fatalf("Complete=%v,%v", got, err)
		}
		delta := r.Stats().Visited - v0
		if delta < 0 || delta > int64(len(got))+1 {
			t.Fatalf("visited delta=%d exceeds commits+1=%d", delta, len(got)+1)
		}
		return delta
	}
	if d10, d100k := measure(10), measure(100_000); d10 != d100k || d10 != 1 {
		t.Fatalf("visited delta at 10=%d at 100000=%d, want equal 1", d10, d100k)
	}
}

// 并发调用等价于某个串行顺序：只读查询在写操作并发进行时
// 始终看到原子状态，记录守恒式成立；恢复三步再单独串行验证。
func TestConcurrentAtomicity(t *testing.T) {
	r, _ := NewRegistry(4, 3)
	var totalMu sync.Mutex
	var total int64
	addTotal := func(n int64) {
		totalMu.Lock()
		total += n
		totalMu.Unlock()
	}
	getTotal := func() int64 {
		totalMu.Lock()
		defer totalMu.Unlock()
		return total
	}
	var wg sync.WaitGroup

	// 纯只读观察者：Pending 有序、Stats 守恒。
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				pending := r.Pending()
				for i := 1; i < len(pending); i++ {
					a, b := pending[i-1], pending[i]
					if a.S > b.S || (a.S == b.S && a.K >= b.K) {
						t.Errorf("pending not sorted: %v", pending)
						return
					}
				}
				// Stats/Pending 必须各自返回自洽快照（不 panic、不撕裂），
				// 守恒式在写线程的同步点上校验。
				_ = r.Stats()
			}
		}()
	}

	// 单写线程串行驱动 Write/Barrier/Complete；原子性由观察者侧验证。
	drive := func(cps int64, completeEvery int64) {
		for k := int64(1); k <= cps; k++ {
			for s := 0; s < r.p; s++ {
				n := int64(int(k)+s%3) + 1
				if err := r.Write(s, n); err == nil {
					addTotal(n)
				}
			}
			if _, err := r.Barrier(k); err != nil {
				t.Fatal(err)
			}
			if completeEvery > 0 && k%completeEvery == 0 {
				if _, err := r.Complete(k); err != nil {
					t.Fatal(err)
				}
				st := r.Stats()
				if st.Committed+st.Aborted+st.Open+st.Prepared != getTotal() {
					t.Fatalf("conservation during drive: %+v vs total %d", st, getTotal())
				}
			}
		}
	}
	drive(24, 3)
	close(stop)
	wg.Wait()

	st := r.Stats()
	if st.Committed+st.Aborted+st.Open+st.Prepared != getTotal() {
		t.Fatalf("final conservation: %+v vs total %d", st, getTotal())
	}

	// 恢复三步原子性：观察线程读不到中间状态，恢复后守恒仍成立。
	observe := make(chan struct{})
	var owg sync.WaitGroup
	owg.Add(2)
	go func() {
		defer owg.Done()
		for {
			select {
			case <-observe:
				return
			default:
			}
			r.Pending()
			_ = r.Stats()
		}
	}()
	go func() {
		defer owg.Done()
		for {
			select {
			case <-observe:
				return
			default:
			}
			if _, err := r.Txn(0, 1); err != nil && !errors.Is(err, ErrNoSuchTxn) {
				t.Errorf("Txn: %v", err)
				return
			}
		}
	}()
	if _, err := r.Restore(24, 2); err != nil {
		t.Fatal(err)
	}
	close(observe)
	owg.Wait()
	st2 := r.Stats()
	if st2.Committed+st2.Aborted+st2.Open+st2.Prepared != getTotal() {
		t.Fatalf("post-restore conservation: %+v vs total %d", st2, getTotal())
	}
}
