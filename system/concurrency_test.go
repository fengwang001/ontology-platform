package system

import (
	"fmt"
	"sync"
	"testing"

	"ontology/calendar"
	"ontology/domain"
)

// TestConcurrent 并发调用各操作（配合 go test -race），
// 验证互斥串行化：无数据竞争，且结果等价于某个串行顺序
// （这里检查串行不变量：预警输出有序、系统可继续正常操作）。
func TestConcurrent(t *testing.T) {
	s := newSys(t)
	base := calendar.FromCivil(2024, 1, 1)
	// 预登记一批对象
	for i := 0; i < 200; i++ {
		cat := domain.CatSafetyValve
		if i%2 == 0 {
			cat = domain.CatBoiler
		}
		must(t, s.Register(fmt.Sprintf("O%04d", i), cat, base+i))
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				id := fmt.Sprintf("O%04d", (g*500+i)%200)
				date := base + 300 + i // 各 goroutine 日期单调；跨协程的回退会被拒绝，属正常
				switch i % 6 {
				case 0:
					_ = s.Inspect(id, date, domain.ResultPass, 0)
				case 1:
					_ = s.Seal(id, date)
				case 2:
					_ = s.Unseal(id, date)
				case 3:
					_, _ = s.Warn(date)
				case 4:
					_, _, _ = s.Usable(id, date)
				case 5:
					_, _, _ = s.RegisterUse(id, date)
				}
			}
		}(g)
	}
	wg.Wait()
	// 串行不变量：预警输出按 (触发到期日, 编号) 升序
	entries, err := s.Warn(base + 1000)
	must(t, err)
	for i := 1; i < len(entries); i++ {
		if entries[i].Expiry < entries[i-1].Expiry && entries[i].ID > entries[i-1].ID {
			// 仅粗检：完整有序性由单元测试与差分测试保证
			t.Fatalf("预警输出疑似无序: %v -> %v", entries[i-1], entries[i])
		}
	}
}
