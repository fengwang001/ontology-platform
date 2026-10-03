package gateway_test

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/gateway"
)

// 并发 Ingest/Drain/Stats：互斥锁保证等价于某个串行顺序；
// 结束后校验全部不变量。配合 go test -race 运行。
func TestConcurrentIngestDrain(t *testing.T) {
	const capB = 1000
	const tenants = 8
	g, _ := gateway.New(capB)
	for i := 0; i < tenants; i++ {
		if err := g.AddTenant(fmt.Sprintf("t%d", i), 1000, 500, i%3); err != nil {
			t.Fatal(err)
		}
	}
	var now atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 500; i++ {
				n := now.Add(int64(rng.Intn(3))) - 1
				switch rng.Intn(4) {
				case 0:
					if _, err := g.Drain(n, int64(rng.Intn(200))); err != nil {
						t.Logf("Drain(%d) -> %v", n, err)
					}
				case 1:
					_ = g.Stats()
					_ = g.Used()
				default:
					name := fmt.Sprintf("t%d", rng.Intn(tenants))
					r, err := g.Ingest(n, name, rng.Intn(3), int64(1+rng.Intn(50)))
					t.Logf("Ingest(%d,%s) -> evicted=%d err=%v", n, name, len(r.Evicted), err)
				}
			}
		}(int64(w))
	}
	wg.Wait()

	if u := g.Used(); u < 0 || u > capB {
		t.Fatalf("used=%d 越界 [0,%d]", u, capB)
	}
	for name, st := range g.Stats() {
		for p := 0; p < 3; p++ {
			if st.AcceptedBytes[p] != st.EvictedBytes[p]+st.DrainedBytes[p]+st.QueuedBytes[p] {
				t.Fatalf("%s prio %d stats=%+v 不变量不成立", name, p, st)
			}
			if st.QueuedBytes[p] < 0 {
				t.Fatalf("%s prio %d queued 为负: %+v", name, p, st)
			}
		}
	}
	for i := 0; i < tenants; i++ {
		tok, _, ok := g.TenantState(fmt.Sprintf("t%d", i))
		if !ok || tok < 0 || tok > 500*1000 {
			t.Fatalf("t%d tokens=%d 越界 [0,500000]", i, tok)
		}
	}
}
