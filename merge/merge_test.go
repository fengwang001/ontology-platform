package merge

import (
	"math"
	"testing"

	"ontology/pwm"
)

// TestReadCounter 证明空闲定位按 last 有序、最小值不做整表扫描：
// 非导出 reads 只在本包测试内读取，公开接口无法触达。
func TestReadCounter(t *testing.T) {
	// 档位一：不引起空闲状态变化、最小值所在分区也不变的 Report，
	// 读数为与 m 无关的常数（peel 读 1 个堆顶 + advance 读 1 个堆顶 = 2）。
	var want int64 = -1
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		m, err := New(n, 1<<60, 0)
		if err != nil {
			t.Fatalf("New(%d): %v", n, err)
		}
		for p := range n {
			m.Report(p, int64(n-p), 0) // p0 水位最高，最小值在 p=n-1
		}
		if _, err := m.Report(0, int64(n)+1, 0); err != nil { // 空闲态、最小值归属均不变
			t.Fatal(err)
		}
		if m.reads != 2 {
			t.Fatalf("n=%d steady reads=%d, want 2", n, m.reads)
		}
		if want < 0 {
			want = m.reads
		} else if m.reads != want {
			t.Fatalf("reads drifted with m: %d vs %d", m.reads, want)
		}
	}

	// 档位二：k 个分区同时转空闲的 Tick，读数恰为 k+2（≤2k+常数），
	// 且实际空闲数恰为 k。k 用循环取多档。
	const n = 1000
	for _, k := range []int{1, 5, 20, 100} {
		m, _ := New(n, 10, 0)
		for p := range n {
			m.Report(p, int64(p+1), 0)
		}
		for p := k; p < n; p++ {
			m.Report(p, int64(p+101), 9) // 后 n-k 个分区 last=9，Tick(10) 保持活跃
		}
		if _, err := m.Tick(10); err != nil {
			t.Fatal(err)
		}
		if m.reads != int64(k+2) {
			t.Fatalf("k=%d reads=%d, want %d", k, m.reads, k+2)
		}
		idle := 0
		for p := range n {
			if m.Idle(p) {
				idle++
			}
		}
		if idle != k {
			t.Fatalf("k=%d actual idle=%d", k, idle)
		}
	}
}

// TestPWM 表驱动核验单分区：上报合法性（相等允许、回退拒绝）与空闲边界。
func TestPWM(t *testing.T) {
	p := pwm.New(0)
	repCases := []struct {
		w    int64
		want bool
	}{{5, true}, {5, true}, {4, false}, {6, true}}
	for i, c := range repCases {
		if got := p.CanReport(c.w); got != c.want {
			t.Fatalf("CanReport case %d (%d)=%v want %v", i, c.w, got, c.want)
		}
		if c.want {
			p.Report(c.w, 6) // 成功上报后 last=6
		}
	}
	if p.Water() != 6 || p.Last() != 6 {
		t.Fatalf("part state=(%d,%d), want (6,6)", p.Water(), p.Last())
	}
	idleCases := []struct {
		now, idle int64
		want      bool
	}{{15, 10, false}, {16, 10, true}, {105, 100, false}, {106, 100, true}}
	for i, c := range idleCases {
		if got := p.Idle(c.now, c.idle); got != c.want {
			t.Fatalf("Idle case %d (%d,%d)=%v want %v", i, c.now, c.idle, got, c.want)
		}
	}
	// 溢出安全：last+idle 越过 int64 上界时阈值不可达，必为非空闲。
	q := pwm.New(math.MaxInt64 - 2)
	if q.Idle(math.MaxInt64, 10) {
		t.Fatal("overflowing threshold must mean active")
	}
}
