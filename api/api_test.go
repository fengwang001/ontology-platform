package api

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"testing"
)

func must(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}
func snapshot(a *API) string { return fmt.Sprint(a.View(), a.Changelog(), a.Discarded()) }

func randomOps(a *API, rng *rand.Rand, n int) {
	for i := 0; i < n; i++ {
		k, fk := fmt.Sprintf("k%d", rng.Intn(20)), fmt.Sprintf("f%d", rng.Intn(4))
		ops := []func(){
			func() { _ = a.PutLeft(k, fk, fmt.Sprintf("v%d", rng.Intn(4))) }, func() { _ = a.PutLeft(k, "", "v") },
			func() { _ = a.DeleteLeft(k) }, func() { _ = a.PutRight(fk, fmt.Sprintf("r%d", rng.Intn(4))) },
			func() { _ = a.DeleteRight(fk) }, func() { _ = a.Deliver(fk) }, // 随机投递；空队列错误忽略
		}
		ops[rng.Intn(len(ops))]()
	}
}

// TestElevenSteps 钉住 NOTES.md 第三节的 11 步推导（不变量 1 的具体实例）。
func TestElevenSteps(t *testing.T) {
	a := New(16)
	must(t, a.PutRight("A", "a1"))
	must(t, a.PutRight("B", "b1"))
	steps := []struct {
		op   func() error
		want string
	}{
		{func() error { return a.PutLeft("k1", "A", "x") }, "[]"}, {func() error { return a.PutLeft("k1", "B", "x") }, "[]"},
		{func() error { return a.Deliver("B") }, "[+k1=(x,b1)]"}, {func() error { return a.Deliver("A") }, "[+k1=(x,b1)]"}, // 第4步丢弃
		{func() error { return a.PutRight("B", "b2") }, "[+k1=(x,b1)]"}, {func() error { return a.PutLeft("k1", "", "x") }, "[+k1=(x,b1) -k1]"},
		{func() error { return a.Deliver("B") }, "[+k1=(x,b1) -k1]"}, {func() error { return a.PutLeft("k2", "B", "y") }, "[+k1=(x,b1) -k1]"}, // 第7步丢弃
		{func() error { return a.DeleteRight("B") }, "[+k1=(x,b1) -k1]"},
		{func() error { return a.Deliver("B") }, "[+k1=(x,b1) -k1 +k2=(y,b2)]"}, {func() error { return a.Deliver("B") }, "[+k1=(x,b1) -k1 +k2=(y,b2) -k2]"},
	}
	for i, s := range steps { // want 为截至本步的累计变更日志
		must(t, s.op())
		if got := fmt.Sprint(a.Changelog()); got != s.want {
			t.Fatalf("step %d: got %s want %s", i+1, got, s.want)
		}
	}
	if len(a.View()) != 0 || a.Discarded() != 2 {
		t.Fatalf("final: view=%v discarded=%d, want empty/2", a.View(), a.Discarded())
	}
}

// TestBatchEquivalence 不变量 1：多档规模、随机投递顺序，DrainAll 后 == 批量重算。
func TestBatchEquivalence(t *testing.T) {
	for _, n := range []int{50, 200, 800} {
		a := New(1 << 20)
		randomOps(a, rand.New(rand.NewSource(int64(n))), n)
		a.DrainAll()
		if rtab, _ := a.r.Snapshot(); !maps.Equal(a.View(), batchJoin(a.l.TabSnapshot(), rtab)) {
			t.Fatalf("n=%d: view != batch recomputation", n)
		}
	}
}

// TestChangelogPrefix 不变量 2：变更日志每个前缀自洽，回放终态 == View。
func TestChangelogPrefix(t *testing.T) {
	a, rng := New(1<<20), rand.New(rand.NewSource(42))
	applied, from, err := map[string][2]string{}, 0, error(nil)
	for step := 0; step < 40; step++ {
		randomOps(a, rng, 10)
		if applied, from, err = applyLog(applied, a.Changelog(), from); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
	}
	a.DrainAll()
	if _, _, err = applyLog(applied, a.Changelog(), from); err != nil || !maps.Equal(applied, a.View()) {
		t.Fatal("changelog prefix/replay violated:", err)
	}
}

func drive(t *testing.T, seed int64, check func(a *API) error) {
	a, rng := New(1<<20), rand.New(rand.NewSource(seed))
	for i := 0; i < 40; i++ {
		randomOps(a, rng, 10)
		must(t, check(a))
	}
}

// TestSubConsistency 不变量 3：任意交错后订阅表与左表严格对应。
func TestSubConsistency(t *testing.T) { drive(t, 9, func(a *API) error { return a.l.CheckSubs() }) }

// TestFaultInjection 不变量 4：三类错误可判定、互不相同、被拒后状态不变且可继续用。
func TestFaultInjection(t *testing.T) {
	a := New(1)
	must(t, a.PutLeft("k1", "A", "x")) // pending=1，占满额度
	snap := snapshot(a)
	for i, c := range [][2]error{{a.PutLeft("", "A", "x"), ErrEmptyKey}, {a.PutRight("", "v"), ErrEmptyKey},
		{a.Deliver("ghost"), ErrEmptyQueue}, {a.PutLeft("k2", "B", "y"), ErrTooManyPending}} {
		if !errors.Is(c[0], c[1]) {
			t.Fatalf("fault %d: got %v want %v", i, c[0], c[1])
		}
	}
	if snap != snapshot(a) {
		t.Fatal("rejected op changed state")
	}
	a.DrainAll()
	must(t, a.PutLeft("k2", "B", "y")) // 被拒后仍可正常使用
}

// TestConcurrentReads 并发只读同一已 DrainAll 实例结果逐条相同；读写并发 -race 干净。
func TestConcurrentReads(t *testing.T) {
	a := New(1 << 20)
	randomOps(a, rand.New(rand.NewSource(3)), 300)
	a.DrainAll()
	want, start, res := snapshot(a), make(chan struct{}), make(chan string, 16*20)
	for i := 0; i < 16; i++ {
		go func() {
			<-start
			for j := 0; j < 20; j++ {
				res <- snapshot(a)
			}
		}()
	}
	close(start)
	for i := 0; i < 16*20; i++ {
		if got := <-res; got != want {
			t.Errorf("reader got %q", got)
		}
	}
	b, done := New(1<<20), make(chan struct{}) // 写/投递与只读、SelfCheck 并发
	for i := 0; i < 4; i++ {
		go func() { randomOps(b, rand.New(rand.NewSource(int64(i))), 100); done <- struct{}{} }()
		go func() {
			for j := 0; j < 50; j++ {
				_, _, _ = b.View(), b.Changelog(), b.Discarded()
				if err := b.SelfCheck(); err != nil {
					t.Error(err)
				}
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
