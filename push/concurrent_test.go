package push

import (
	"sync"
	"testing"

	"ontology/group"
)

// TestConcurrentSafe 并发混合调用：不发生数据竞争，结束后全局不变量成立。
func TestConcurrentSafe(t *testing.T) {
	s := New(16)
	must(t, s.SetPolicy(group.Star, pol("k", "v0")), "star")
	must(t, s.AddGroup("g", 10), "g")
	must(t, s.SetPolicy("g", pol("k", "v1")), "gp")

	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				d := group.Name("d" + itoa((w*200+i)%40))
				switch i % 8 {
				case 0:
					_ = s.AddDevice(d)
				case 1:
					_ = s.AddMember("g", d)
				case 2:
					_ = s.RemoveMember("g", d)
				case 3:
					if pid := s.PendingID(d); pid != 0 {
						_ = s.Ack(d, pid)
					}
				case 4:
					if pid := s.PendingID(d); pid != 0 {
						_ = s.Nack(d, pid)
					}
				case 5:
					_ = s.SetPolicy("g", pol("k", "v"+itoa(i%3)))
				case 6:
					_ = s.SetPriority("g", 10+i%5)
				case 7:
					_ = s.RemoveDevice(d)
				}
				_ = s.EffectiveConfig(d)
			}
		}(w)
	}
	wg.Wait()

	if msg := s.CheckInvariant(); msg != "" {
		t.Fatalf("invariant after concurrent load: %s", msg)
	}
}
