package lag

import (
	"runtime"
	"testing"

	"ontology/fresh"
)

func newReader(t *testing.T, commits int64) *Reader {
	t.Helper()
	r := NewReader(fresh.New())
	for i := int64(0); i <= commits; i++ {
		if err := r.Commit(i); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// 唤醒走按 T 排序的最小堆：1 个小 T + m-1 个大 T，一次 Apply 只唤醒那 1 个，
// 检查过的堆顶条目数不随 m 线性增长。
func TestWakeChecksBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := newReader(t, 9)
		done := make(chan struct{}, m)
		go func() { _, _ = r.Read(9, Block); done <- struct{}{} }() // 1 个 T=0
		for i := 0; i < m-1; i++ {
			go func() { _, _ = r.Read(0, Block); done <- struct{}{} }() // m-1 个 T=9
		}
		for r.waiters() < m { // 不用 sleep，自旋等全部入堆
			runtime.Gosched()
		}
		if err := r.Apply(0); err != nil { // 恰好只唤醒 T=0 那 1 个
			t.Fatal(err)
		}
		if r.checked > 3 { // 弹出 1 个 + 检查下一个堆顶，常数级
			t.Fatalf("m=%d: checked=%d，随 m 线性增长", m, r.checked)
		}
		if err := r.Apply(9); err != nil { // 放行其余等待者，避免泄漏
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			<-done
		}
	}
}

// m 个阻塞者各自冻结不同的 T，Apply 逐步推进后全部按各自 T 正确放行。
func TestConcurrentBlockWake(t *testing.T) {
	for _, m := range []int64{8, 64, 256} {
		r := newReader(t, m)
		type out struct{ t, pos int64 }
		res := make(chan out, m)
		for tt := int64(0); tt < m; tt++ {
			go func() {
				got, err := r.Read(m-tt, Block) // 冻结 T = m-(m-tt) = tt
				if err != nil || got.Downgraded {
					t.Error("Block 读异常:", got, err)
				}
				res <- out{tt, got.Pos}
			}()
		}
		for r.waiters() < int(m) {
			runtime.Gosched()
		}
		for n := int64(0); n <= m; n++ { // 不用 sleep，逐个 Apply 放行
			if err := r.Apply(n); err != nil {
				t.Fatal(err)
			}
		}
		for i := int64(0); i < m; i++ {
			if got := <-res; got.pos < got.t {
				t.Fatalf("m=%d: 等待者 T=%d 拿到 Pos=%d，滞后于冻结目标", m, got.t, got.pos)
			}
		}
		if r.waiters() != 0 {
			t.Fatalf("m=%d: 放行后仍有 %d 个等待者", m, r.waiters())
		}
	}
}
