package lab

import (
	"fmt"
	"testing"
	"time"
)

// 复杂度验证：签收一个管的开销只与管内项目数相关，
// 查询患者未终结项目的开销只与未终结项目数相关。
// 通过 Metrics.ItemsScanned（访问的申请项记录数）在两档历史规模上对照，
// 计数与历史总量无关即为可验证的证明；同时打印墙钟时间作为参考。

const (
	complexityTubeItems  = 5 // 待签收管内的项目数 M
	complexityUnfinished = 7 // 额外保持未终结的项目数 U
	complexityMaxDeliver = 1 << 40
)

// buildHistory 构造一名患者：finished 个已终结项目 + unfinished 个待采集项目
// + 一支含 tubeItems 个项目的待签收管。返回系统与当前时刻。
func buildHistory(tb testing.TB, finished, unfinished, tubeItems int) (*System, int64) {
	tb.Helper()
	s := NewSystem()
	total := finished + unfinished + tubeItems
	for i := 0; i < total; i++ {
		if err := s.RegisterItem(0, fmt.Sprintf("item%d", i), "tubeA", complexityMaxDeliver, false, 4); err != nil {
			tb.Fatal(err)
		}
	}
	now := int64(1)
	idx := 0
	next := func() string { id := fmt.Sprintf("item%d", idx); idx++; return id }
	// 历史：每 10 个项目一组申请、采集、签收为合格（终结）。
	for idx < finished {
		var items []string
		for j := 0; j < 10 && idx < finished; j++ {
			items = append(items, next())
		}
		appID := fmt.Sprintf("app-h%d", idx)
		if err := s.SubmitApplication(now, appID, "p1", items, 1); err != nil {
			tb.Fatal(err)
		}
		tubeID := fmt.Sprintf("tube-h%d", idx)
		if err := s.Collect(now, tubeID, "tubeA", "p1", items, now); err != nil {
			tb.Fatal(err)
		}
		if _, err := s.Sign(now, tubeID, 0); err != nil {
			tb.Fatal(err)
		}
		now++
	}
	// 未终结（待采集）项目。
	var pending []string
	for j := 0; j < unfinished; j++ {
		pending = append(pending, next())
	}
	if len(pending) > 0 {
		if err := s.SubmitApplication(now, "app-pending", "p1", pending, 1); err != nil {
			tb.Fatal(err)
		}
	}
	now++
	// 待签收管。
	var inTube []string
	for j := 0; j < tubeItems; j++ {
		inTube = append(inTube, next())
	}
	if err := s.SubmitApplication(now, "app-tube", "p1", inTube, 1); err != nil {
		tb.Fatal(err)
	}
	if err := s.Collect(now, "tube-target", "tubeA", "p1", inTube, now); err != nil {
		tb.Fatal(err)
	}
	now++
	return s, now
}

func TestSignAndQueryComplexityTwoScales(t *testing.T) {
	scales := []int{2_000, 40_000}
	type measurement struct {
		signScanned, queryScannedBefore, queryScannedAfter int64
		signDur, queryDur                                  time.Duration
	}
	var results []measurement
	for _, n := range scales {
		s, now := buildHistory(t, n, complexityUnfinished, complexityTubeItems)

		s.Metrics.ResetItemsScanned()
		start := time.Now()
		if _, err := s.Sign(now, "tube-target", 0); err != nil {
			t.Fatal(err)
		}
		signDur := time.Since(start)
		signScanned := s.Metrics.ItemsScanned()

		s.Metrics.ResetItemsScanned()
		start = time.Now()
		if _, err := s.QueryPatient(now, "p1"); err != nil {
			t.Fatal(err)
		}
		queryDur := time.Since(start)
		queryScanned := s.Metrics.ItemsScanned()

		results = append(results, measurement{
			signScanned: signScanned, queryScannedBefore: queryScanned,
			signDur: signDur, queryDur: queryDur,
		})
		t.Logf("历史规模=%d: 签收扫描=%d(管内项目=%d) 耗时=%v; 查询扫描=%d(未终结=%d) 耗时=%v",
			n, signScanned, complexityTubeItems, signDur,
			queryScanned, complexityUnfinished, queryDur)
	}
	for i, n := range scales {
		m := results[i]
		if m.signScanned != complexityTubeItems {
			t.Fatalf("历史规模=%d: 签收扫描数 %d 应等于管内项目数 %d", n, m.signScanned, complexityTubeItems)
		}
		if m.queryScannedBefore != complexityUnfinished {
			t.Fatalf("历史规模=%d: 查询扫描数 %d 应等于未终结项目数 %d", n, m.queryScannedBefore, complexityUnfinished)
		}
	}
	if results[0].signScanned != results[1].signScanned || results[0].queryScannedBefore != results[1].queryScannedBefore {
		t.Fatalf("两档规模的扫描数应完全相同: %+v vs %+v", results[0], results[1])
	}
}

// BenchmarkQueryPatient 在两档历史规模上测量患者未终结项目查询。
func BenchmarkQueryPatient(b *testing.B) {
	for _, n := range []int{2_000, 40_000} {
		s, now := buildHistory(b, n, complexityUnfinished, complexityTubeItems)
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := s.QueryPatient(now, "p1"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSign 在两档历史规模上测量单管签收。
func BenchmarkSign(b *testing.B) {
	for _, n := range []int{2_000, 40_000} {
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				s, now := buildHistory(b, n, 0, complexityTubeItems)
				b.StartTimer()
				if _, err := s.Sign(now, "tube-target", 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
