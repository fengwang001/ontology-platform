package session

import (
	"sync"
	"testing"

	"ontology/topo"
)

// TestConcurrentOperations 高并发混合操作下以 race 检测器验证互斥安全，
// 并在结束后校验“在线蕴含父在线”。
func TestConcurrentOperations(t *testing.T) {
	f := newFixture(t, 100)
	for _, name := range []string{"G1", "G2"} {
		if err := f.g.AddNode(name, topo.KindGateway); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 8; i++ {
		name := string(rune('a' + i))
		if err := f.g.AddNode(name, topo.KindDevice); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				switch k % 5 {
				case 0:
					f.m.Online("G1", "", 0)
				case 1:
					name := string(rune('a' + (id+k)%8))
					if ep, ok := f.m.EpochOf("G1"); ok {
						f.m.Online(name, "G1", ep)
					}
				case 2:
					if ep, ok := f.m.EpochOf("G1"); ok {
						f.m.Offline("G1", ep)
					}
				case 3:
					f.m.IsOnline("G1")
				case 4:
					f.g.ChildCount("G1")
				}
			}
		}(w)
	}
	wg.Wait()

	f.g.RLock()
	defer f.g.RUnlock()
	for name := range f.m.online {
		if p, bound := f.g.ParentOf(name); bound {
			if _, on := f.m.online[p]; !on {
				t.Fatalf("%s online while parent %s offline", name, p)
			}
		}
	}
}
