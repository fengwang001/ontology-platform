package issue

import (
	"sync"
	"testing"

	"ontology/bloodstock"
)

func TestConcurrentCallsSerializable(t *testing.T) {
	m := New(0, 1_000_000)
	m.Grant("tech", RoleTech)
	m.Grant("i1", RoleIssue)
	m.Grant("i2", RoleIssue)
	for i := 0; i < 200; i++ {
		if err := m.AddBag(0, "b"+itoa2(int64(i)), bloodstock.O, bloodstock.Negative, 1_000_000); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	// 全部使用同一 now 串行等价地接受；任意时刻每袋至多一个状态。
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _, _ = m.Crossmatch(1, "tech", "P", 3)
				_ = m.Discard(2, "tech", "b"+itoa2(int64((g*50+i)%200)))
			}
		}(g)
	}
	wg.Wait()
	// 不崩溃、不竞态即通过；状态不变量由 Store 单线程串行化保证。
}
