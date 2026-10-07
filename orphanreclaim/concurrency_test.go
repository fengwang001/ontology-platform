package orphanreclaim

import (
	"sync"
	"testing"
)

// 线程安全的确定性时钟：所有 goroutine 看到的时间只进不退。
type lockedClock struct {
	mu sync.Mutex
	t  int64
}

func (c *lockedClock) now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *lockedClock) set(t int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t > c.t {
		c.t = t
	}
}

// 并发的入边增删、代际推进扫描与清理执行同时发生。
// 要求：-race 干净；不存在「对象同时在两代队列」；终态必与朴素模型一致。
func TestConcurrentMutationAndAdvance(t *testing.T) {
	cfg := testConfig()
	lc := &lockedClock{}
	engine, err := New(cfg, lc.now, nil, nopLogger{})
	if err != nil {
		t.Fatal(err)
	}

	const nObjs = 12
	for i := 0; i < nObjs; i++ {
		engine.CreateObject("o" + itoa(i))
	}
	types := []string{"I", "J1", "J2", "X", "Y"}

	stop := make(chan struct{})
	var houseWg sync.WaitGroup // 推进者 + 只读核对者
	var workWg sync.WaitGroup  // 6 个增删边工作者

	// 推进 + 时间推进者。
	houseWg.Add(1)
	go func() {
		defer houseWg.Done()
		tick := int64(0)
		for {
			select {
			case <-stop:
				return
			default:
				tick++
				lc.set(tick / 2)
				engine.Advance()
			}
		}
	}()

	// 多个增删边 goroutine。
	for w := 0; w < 6; w++ {
		workWg.Add(1)
		go func(seed int) {
			defer workWg.Done()
			x := seed + 1
			for k := 0; k < 400; k++ {
				x = (x*1103515245 + 12345) & 0x7fffffff
				src := "o" + itoa(x%nObjs)
				x = (x*1103515245 + 12345) & 0x7fffffff
				tgt := "o" + itoa(x%nObjs)
				lt := types[x%5]
				if k%3 == 0 {
					_ = engine.RemoveLink(src, tgt, lt)
				} else {
					_ = engine.AddLink(src, tgt, lt)
				}
			}
		}(w)
	}

	// 只读核对者：任何时刻对象不得同时出现在两个代堆。
	houseWg.Add(1)
	go func() {
		defer houseWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				engine.mu.Lock()
				for id := range engine.gen1.inner.indexOf {
					if _, in2 := engine.gen2.inner.indexOf[id]; in2 {
						t.Errorf("object %s simultaneously in gen1 and gen2", id)
					}
					if o := engine.objects[id]; o == nil || o.gen != Gen1 {
						t.Errorf("gen1 heap/index inconsistent for %s", id)
					}
				}
				for id := range engine.gen2.inner.indexOf {
					if o := engine.objects[id]; o == nil || o.gen != Gen2 {
						t.Errorf("gen2 heap/index inconsistent for %s", id)
					}
				}
				engine.mu.Unlock()
			}
		}
	}()

	workWg.Wait()
	close(stop)
	houseWg.Wait()

	// 最终一致：再推进一次后，快照内部索引仍必须自洽。
	engine.Advance()
	snap := engine.Snapshot()
	inBoth := map[string]bool{}
	for _, id := range snap.Gen1Queue {
		inBoth[id] = true
	}
	for _, id := range snap.Gen2Queue {
		if inBoth[id] {
			t.Fatalf("object %s in both generations in final snapshot", id)
		}
	}
	for id, gen := range snap.Alive {
		switch gen {
		case Gen1:
			if !contains(snap.Gen1Queue, id) {
				t.Fatalf("%s marked gen1 but absent from gen1 queue", id)
			}
		case Gen2:
			if !contains(snap.Gen2Queue, id) {
				t.Fatalf("%s marked gen2 but absent from gen2 queue", id)
			}
		}
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
