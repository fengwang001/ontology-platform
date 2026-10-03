package negotiate

import (
	"sync"
	"testing"

	"ontology/adapter"
)

// TestConcurrentStress 并发混合变更与协商：
// 协商只读且基于单个快照，任何结果必须自洽，版本号单调且无数据竞争。
func TestConcurrentStress(t *testing.T) {
	r, err := adapter.New(20)
	if err != nil {
		t.Fatal(err)
	}
	n := New(r)

	for v := 1; v < 20; v++ {
		must(t, r.SetAdapter(v, v%3 == 0, v%4 == 0))
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for i := 0; i < 500; i++ {
				v := 1 + (i+id)%19
				if (i+id)%2 == 0 {
					_ = r.SetAdapter(v, i%5 == 0, i%7 == 0)
				} else {
					_ = r.RemoveAdapter(v)
					_ = r.SetAdapter(v, i%5 == 0, i%7 == 0)
				}
				_ = r.Sunset(v, int64(100+(i%150)))
			}
		}(g)
	}

	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for i := 0; i < 1000; i++ {
				plan, err := n.Negotiate("1-20", i%3 != 0,
					[]string{"alpha", "beta"}, int64(i%250))
				if err == nil {
					if plan.Version < 1 || plan.Version > 20 {
						t.Errorf("计划版本越界：%d", plan.Version)
						return
					}
					if len(plan.Steps) != 20-plan.Version {
						t.Errorf("步骤长度 %d != H-v %d", len(plan.Steps), 20-plan.Version)
						return
					}
				}
			}
		}(g)
	}

	close(start)
	wg.Wait()

	rev := r.Revision()
	snap := r.SnapshotAt()
	if snap.Rev != rev {
		t.Fatalf("并发结束后快照版本号 %d 与注册表 %d 不一致", snap.Rev, rev)
	}
	t.Logf("并发混合读写完成，最终注册表版本号=%d，所有协商计划自洽", rev)
}
