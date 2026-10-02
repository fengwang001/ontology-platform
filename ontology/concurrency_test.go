package ontology

import (
	"sync"
	"testing"
)

// 并发混合调用：写操作与 Verify 同时进行，必须无竞态、不 panic。
func TestConcurrent(t *testing.T) {
	v := mustNew(t, 16, 10_000)
	for i := 0; i < 100; i++ {
		mustAdd(t, v, mkCert("root", "root", "root", "kr", "kr", 0, 1000, true, -1))
		break // 根只登记一次
	}
	mustAdd(t, v, mkCert("mid", "mid", "root", "km", "kr", 0, 1000, true, -1))
	leaf := mkCert("leaf", "leaf", "mid", "kl", "km", 0, 1000, false, -1)
	leaf.SAN = []string{"h.example.com"}
	mustAdd(t, v, leaf)
	mustTrust(t, v, "root")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				id := "extra-" + itoa(i*200+j)
				_ = v.Add(mkCert(id, "sx-"+itoa(j), "sx-"+itoa(j), id, id, 0, 1000, true, -1))
				_, _ = v.Verify([]byte("leaf"), "h.example.com", 10)
			}
		}(i)
	}
	wg.Wait()
	r, err := v.Verify([]byte("leaf"), "h.example.com", 10)
	if err != nil || r.Failure != nil || r.NoPath {
		t.Fatalf("post-concurrency verify: %+v err=%v", r, err)
	}
	if r.Considered != 2 {
		t.Fatalf("considered after concurrent noise = %d, want 2", r.Considered)
	}
}
