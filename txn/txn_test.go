package txn

import (
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// naive 是朴素参照：按同一组规则维护 store+C，用于对照。
type naive struct {
	m map[int64]int64
	c int64
}

func (n *naive) apply(seq, eff int64) {
	if seq > 0 && seq == n.c+1 {
		n.m[seq] = eff
	}
}

func (n *naive) commit(seq int64) {
	if seq > 0 && seq == n.c+1 {
		if _, ok := n.m[seq]; ok {
			n.c = seq
		}
	}
}

func (n *naive) pending() []int64 {
	out := []int64{}
	for s := range n.m {
		if s > n.c {
			out = append(out, s)
		}
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// 不变量1（不丢）：随机操作序列重放后与朴素参照逐项一致，
// 且任意 seq<=C 的效果都在 store 里。
func TestNoLossVsNaive(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		tx, nf := New(), &naive{m: map[int64]int64{}}
		r := rand.New(rand.NewSource(seed))
		for i := 0; i < 300; i++ {
			seq := int64(r.Intn(int(nf.c + 4)))
			switch r.Intn(4) {
			case 0, 1:
				eff := int64(r.Intn(1000))
				tx.Apply(seq, eff)
				nf.apply(seq, eff)
			case 2:
				tx.Commit(seq)
				nf.commit(seq)
			default:
				if _, err := tx.Restart(); err != nil {
					t.Fatalf("seed %d: Restart: %v", seed, err)
				}
			}
			snap := tx.Snapshot()
			for s := int64(1); s <= tx.Committed(); s++ {
				if _, ok := snap[s]; !ok {
					t.Fatalf("seed %d step %d: committed effect %d lost", seed, i, s)
				}
			}
		}
		if tx.Committed() != nf.c || !reflect.DeepEqual(tx.Snapshot(), nf.m) ||
			!reflect.DeepEqual(tx.Pending(), nf.pending()) {
			t.Fatalf("seed %d: diverged from naive reference", seed)
		}
	}
}

// 不变量2（位点连续）：表驱动，seq>C+1 的 Commit 一律 ErrOffsetJump，C 不动。
func TestOffsetJumpRejected(t *testing.T) {
	for _, c := range []struct {
		advanceTo int64
		jumpTo    int64
	}{{0, 2}, {0, 5}, {3, 5}, {3, 100}} {
		tx := New()
		for i := int64(1); i <= c.advanceTo; i++ {
			tx.Apply(i, i)
			tx.Commit(i)
		}
		if err := tx.Commit(c.jumpTo); err != ErrOffsetJump {
			t.Fatalf("%+v: got %v want ErrOffsetJump", c, err)
		}
		if tx.Committed() != c.advanceTo {
			t.Fatalf("%+v: C moved to %d", c, tx.Committed())
		}
	}
}

// 不变量3（幂等重放）：重复 Apply 覆盖不改值；崩溃重放后值只取决于最后一次 Apply。
func TestIdempotentReplay(t *testing.T) {
	tx := New()
	for i := 0; i < 5; i++ {
		if err := tx.Apply(1, 20); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := tx.Snapshot()[1]; got != 20 {
		t.Fatalf("store[1]=%d want 20", got)
	}
	if _, err := tx.Restart(); err != nil {
		t.Fatal(err)
	}
	tx.Apply(1, 20) // 崩溃后 re-Apply，仍幂等
	tx.Commit(1)
	tx.Commit(1)    // 幂等
	tx.Apply(1, 99) // 已提交序号：幂等空操作
	if got := tx.Snapshot()[1]; got != 20 || tx.Committed() != 1 {
		t.Fatalf("store[1]=%d C=%d, want 20/1", got, tx.Committed())
	}
}

// 不变量4（失败不留痕）：四类错误互不相同，被拒后 store/C 不变且可继续使用。
func TestFailureLeavesNoTrace(t *testing.T) {
	tx := New()
	before := tx.Snapshot()
	errs := []error{
		tx.Apply(0, 1), tx.Apply(-3, 1), // ErrInvalidSeq
		tx.Apply(3, 1), // ErrOutOfOrder
		tx.Commit(1),   // ErrEffectMissing（store 无 1）
		tx.Commit(7),   // ErrOffsetJump
		tx.Commit(0),   // ErrInvalidSeq
	}
	want := []error{ErrInvalidSeq, ErrInvalidSeq, ErrOutOfOrder, ErrEffectMissing, ErrOffsetJump, ErrInvalidSeq}
	seen := map[error]bool{}
	for i, err := range errs {
		if err != want[i] {
			t.Fatalf("op %d: got %v want %v", i, err, want[i])
		}
		seen[err] = true
	}
	if len(seen) != 4 {
		t.Fatalf("sentinel errors not distinct: %v", seen)
	}
	if tx.Committed() != 0 || !reflect.DeepEqual(tx.Snapshot(), before) {
		t.Fatal("rejected ops left trace")
	}
	if err := tx.Apply(1, 10); err != nil {
		t.Fatal("unusable after rejections")
	}
	if err := tx.Commit(1); err != nil || tx.Committed() != 1 {
		t.Fatal("unusable after rejections")
	}
}

// 复杂度：推进 C 的 Commit 只检查 C+1 一处，不随已提交前缀线性增长。
func TestCommitChecksO1(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		tx := New()
		for i := int64(1); i <= m; i++ {
			tx.Apply(i, i)
			tx.Commit(i)
		}
		tx.Apply(m+1, m+1)
		if err := tx.Commit(m + 1); err != nil {
			t.Fatal(err)
		}
		if tx.lastChecked > 2 {
			t.Fatalf("m=%d: commit checked %d entries, want O(1)", m, tx.lastChecked)
		}
	}
}

// 并发：N 个 goroutine 对同一在途序号并发幂等 Apply，随后一次 Commit；
// C 恰推进 1，期间 Committed() 单调不减。
func TestConcurrentApplyCommit(t *testing.T) {
	for _, n := range []int{2, 8, 32} {
		tx := New()
		start := make(chan struct{})
		var wg sync.WaitGroup
		var maxSeen, monotonic atomic.Int64
		monotonic.Store(1)
		stop := make(chan struct{})
		go func() { // 读侧：校验 Committed 单调不减
			last := int64(0)
			for {
				select {
				case <-stop:
					return
				default:
					cur := tx.Committed()
					if cur < last {
						monotonic.Store(0)
					}
					if cur > maxSeen.Load() {
						maxSeen.Store(cur)
					}
					last = cur
				}
			}
		}()
		for g := 0; g < n; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for k := 0; k < 200; k++ {
					if err := tx.Apply(1, 42); err != nil {
						t.Error(err)
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		if err := tx.Commit(1); err != nil {
			t.Fatal(err)
		}
		close(stop)
		if tx.Committed() != 1 || tx.Snapshot()[1] != 42 || monotonic.Load() != 1 {
			t.Fatalf("n=%d: C=%d store[1]=%d monotonic=%d",
				n, tx.Committed(), tx.Snapshot()[1], monotonic.Load())
		}
	}
}
