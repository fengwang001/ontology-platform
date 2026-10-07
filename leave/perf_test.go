package leave_test

import (
	"math/rand"
	"runtime"
	"testing"
	"time"

	"ontology/leave"
)

// perfConfig：额度足够大，申请总能成功。
func perfConfig() leave.Config {
	return leave.Config{
		TenureBounds:  []int{1},
		AnnualQuotas:  []int{1 << 28, 1 << 28},
		CarryCap:      1 << 28,
		CarryDeadline: 300,
	}
}

type perfResult struct {
	n            int
	reqApproveNs float64
	earlyEndNs   float64
	balanceNs    float64
}

// runPerfScale 在历史请假记录规模为 n 的员工上测量三类操作的单次平均耗时。
func runPerfScale(t *testing.T, n int) perfResult {
	t.Helper()
	s := newService(t, perfConfig())
	mustRegister(t, s, 0, "e", 0)

	// 构建 n 张已批准假单：[3i, 3i+1]，now 单调推进，记录假单 ID。
	ids := make([]int64, 0, n+3000)
	build := func(count, startIdx int) {
		for i := startIdx; i < startIdx+count; i++ {
			d := 3 * i
			lid, _, err := s.RequestLeave(d, "e", d, d+1)
			if err != nil {
				t.Fatalf("build request %d: %v", i, err)
			}
			if err := s.Approve(d+1, "e", lid); err != nil {
				t.Fatalf("build approve %d: %v", i, err)
			}
			ids = append(ids, lid)
		}
	}
	build(n, 0)

	const sample = 2000
	lastNow := 3*(n+sample) + 10

	// A：申请+批准（在历史规模 n 之上继续追加 sample 张）
	runtime.GC()
	t0 := time.Now()
	build(sample, n)
	reqApprove := time.Since(t0)

	// B：提前结束（对 A 阶段产生的 sample 张假单）
	runtime.GC()
	t0 = time.Now()
	for i := 0; i < sample; i++ {
		idx := n + i
		d := 3 * idx
		if err := s.EarlyEnd(lastNow+i, "e", ids[idx], d); err != nil {
			t.Fatalf("early end %d: %v", idx, err)
		}
	}
	earlyEnd := time.Since(t0)
	lastNow += sample

	// C：历史时刻余额查询（随机历史 now）
	rng := rand.New(rand.NewSource(42))
	runtime.GC()
	t0 = time.Now()
	for i := 0; i < sample; i++ {
		q := rng.Intn(lastNow + 1)
		if _, err := s.Balance(q, "e"); err != nil {
			t.Fatalf("balance %d: %v", q, err)
		}
	}
	balance := time.Since(t0)

	return perfResult{
		n:            n,
		reqApproveNs: float64(reqApprove.Nanoseconds()) / sample,
		earlyEndNs:   float64(earlyEnd.Nanoseconds()) / sample,
		balanceNs:    float64(balance.Nanoseconds()) / sample,
	}
}

// TestPerfScaleIndependence 证明申请、销假、余额查询的开销不随员工历史
// 请假记录总数增长：两档规模相差两个数量级，单次耗时应基本相当。
func TestPerfScaleIndependence(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	small := runPerfScale(t, 2_000)
	large := runPerfScale(t, 200_000)

	t.Logf("scale n=%d:    request+approve %.0f ns/op, early-end %.0f ns/op, balance %.0f ns/op",
		small.n, small.reqApproveNs, small.earlyEndNs, small.balanceNs)
	t.Logf("scale n=%d: request+approve %.0f ns/op, early-end %.0f ns/op, balance %.0f ns/op",
		large.n, large.reqApproveNs, large.earlyEndNs, large.balanceNs)

	ratio := func(a, b float64) float64 { return a / b }
	t.Logf("ratio(100x): request+approve x%.2f, early-end x%.2f, balance x%.2f",
		ratio(large.reqApproveNs, small.reqApproveNs),
		ratio(large.earlyEndNs, small.earlyEndNs),
		ratio(large.balanceNs, small.balanceNs))

	// 允许常数倍的测量噪声（含 log n 因子），但不允许线性增长。
	const limit = 5.0
	if ratio(large.reqApproveNs, small.reqApproveNs) > limit {
		t.Fatalf("request+approve grows with history: x%.2f", ratio(large.reqApproveNs, small.reqApproveNs))
	}
	if ratio(large.earlyEndNs, small.earlyEndNs) > limit {
		t.Fatalf("early-end grows with history: x%.2f", ratio(large.earlyEndNs, small.earlyEndNs))
	}
	if ratio(large.balanceNs, small.balanceNs) > limit {
		t.Fatalf("balance grows with history: x%.2f", ratio(large.balanceNs, small.balanceNs))
	}
}
