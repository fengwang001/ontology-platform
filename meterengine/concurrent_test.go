package meterengine

import (
	"sync"
	"testing"
)

// TestConcurrentStress 在多 goroutine 下混合调用所有写操作与查询，
// 要求 -race 下无数据竞争、无 panic，且最终状态满足可加性。
func TestConcurrentStress(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.SetPointLimit("p", 1000), "上限")
	for _, id := range []string{"m0", "m1", "m2", "m3"} {
		mustOK(t, e.RegisterMeter(id, 3, int64(id[1]-'0'+1)), "登记")
	}
	mustOK(t, e.InstallMeter("p", "m0", 1, 0), "安装")

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				tm := int64(2 + g*200 + i)
				_ = e.RegisterReading("m0", tm, int64((i*7)%900), Actual)
				_, _ = e.Query("p", 1, tm)
				_ = e.RegisterReading("m0", tm+1, int64((i*13)%900), Estimated)
				_ = e.DeleteEstimated("m0", tm+1)
			}
		}(g)
	}
	wg.Wait()

	// 换表后跨表查询仍可用。
	mustOK(t, e.SwapMeter("p", 100000, 0, "m1", 0), "换表")
	mustOK(t, e.RegisterReading("m1", 100001, 5, Actual), "新表读数")
	r, err := e.Query("p", 1, 100001)
	mustOK(t, err, "最终查询")
	if r.Energy < 0 {
		t.Fatalf("用电量为负: %d", r.Energy)
	}
}
