package medsched

import (
	"fmt"
	"testing"
)

// buildAgedInterval 构造一个固定间隔医嘱：anchor=0, H=h, 已“走过” ageSec 秒
// 历史（不做任何处理记录，只让序列在数学上延伸到 ageSec）。查询始终落在当前
// 时钟 now=ageSec 附近的一个固定宽度窗口，因此返回点数相同。
func buildAgedInterval(b *testing.B, w, h, ageSec int64) (*System, string) {
	b.Helper()
	s, err := New(w)
	if err != nil {
		b.Fatal(err)
	}
	if err := s.RegisterDrug(0, "A", "c", 1); err != nil {
		b.Fatal(err)
	}
	id, err := s.OpenOrder(OpenOrderInput{
		Now:       0,
		Patient:   "p",
		Drug:      "A",
		Frequency: Frequency{Kind: FreqInterval, First: 0, H: h},
	})
	if err != nil {
		b.Fatal(err)
	}
	return s, id
}

// benchmarkQueryAged 查询 now 附近宽度 span 的窗口；跨度内点数与 ageSec 无关。
func benchmarkQueryAged(b *testing.B, ageSec int64) {
	const w = int64(10)
	const h = int64(100)
	const span = int64(1000)
	s, _ := buildAgedInterval(b, w, h, ageSec)
	lo := ageSec
	hi := ageSec + span
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pts, err := s.Query(ageSec+span, "p", lo, hi)
		if err != nil {
			b.Fatal(err)
		}
		if len(pts) == 0 {
			b.Fatal("expected points")
		}
	}
}

// 短历史：医嘱刚运行，历史点 ~10 个。
func BenchmarkQueryShortHistory(b *testing.B) { benchmarkQueryAged(b, 1000) }

// 长历史：医嘱已走过约 1e8 秒（约 3 年），H=100 => 1e6 个历史点。
func BenchmarkQueryLongHistory(b *testing.B) { benchmarkQueryAged(b, 100_000_000) }

// TestQueryCostIndependence 以可验证方式断言：同一宽度窗口的查询返回点数
// 相同且耗时不随历史点总数增长（长档耗时不超过短档的一个宽松常数倍）。
func TestQueryCostIndependence(t *testing.T) {
	cases := []struct {
		name    string
		ageSec  int64
		histPts int64
	}{
		{"short", 1_000, 10},
		{"long", 100_000_000, 1_000_000},
	}
	var ns [2]int64
	var np [2]int
	for i, c := range cases {
		res := testing.Benchmark(func(b *testing.B) {
			benchmarkQueryAged(b, c.ageSec)
		})
		ns[i] = res.NsPerOp()
		// 直接验证一次返回点数。
		sys, err := New(10)
		if err != nil {
			t.Fatal(err)
		}
		if err := sys.RegisterDrug(0, "A", "c", 1); err != nil {
			t.Fatal(err)
		}
		if _, err := sys.OpenOrder(OpenOrderInput{Now: 0, Patient: "p", Drug: "A",
			Frequency: Frequency{Kind: FreqInterval, First: 0, H: 100}}); err != nil {
			t.Fatal(err)
		}
		pts, err := sys.Query(c.ageSec+1000, "p", c.ageSec, c.ageSec+1000)
		if err != nil {
			t.Fatal(err)
		}
		np[i] = len(pts)
		t.Logf("%s: 历史点约=%d, 单次查询=%d ns/op, 返回点数=%d",
			c.name, c.histPts, ns[i], np[i])
	}
	if np[0] != np[1] {
		t.Fatalf("两档返回点数应相同: %d vs %d", np[0], np[1])
	}
	// 长档不应显著慢于短档（宽松 8 倍，容纳噪声）。
	if ns[1] > 8*ns[0] {
		t.Fatalf("查询耗时随历史增长: short=%dns long=%dns", ns[0], ns[1])
	}
	fmt.Printf("查询开销对照: 短历史=%dns/次, 长历史(1e6点)=%dns/次\n", ns[0], ns[1])
}
