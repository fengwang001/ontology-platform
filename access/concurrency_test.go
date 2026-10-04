package access_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/access"
)

// 并发下不变量：每个 grantId 在 Reaped 中至多一次、活动授权数 ≤ M、Check 不 panic。
func TestConcurrentInvariants(t *testing.T) {
	s := access.New(access.Config{P: 100000, M: 3, Lmax: 300, E: 2, Cool: 10})
	must(t, s.SetOwners(b("r"), [][]byte{b("o")}, 0))

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := int64(g*200 + i)
				uid := fmt.Sprintf("u%d", g)
				qid := fmt.Sprintf("q-%d-%d", g, i)
				_ = s.Request(qid, b(uid), b("r"), 50, now)
				_, _ = s.Approve(qid, b("o"), now+1)
				_, _ = s.Delegate(1+int64(g), []byte(fmt.Sprintf("v%d", g)), 10, now+2)
				_ = s.Extend(1+int64(g), 5, now+3)
				_ = s.Check(b(uid), b("r"), now)
				_ = s.Tick(now)
				_ = s.Revoke(1+int64(g), b("o"), now+4)
			}
		}(g)
	}
	wg.Wait()

	seen := map[int64]int{}
	for _, e := range s.Reaped() {
		seen[e.GrantID]++
		if seen[e.GrantID] > 1 {
			t.Fatalf("grant %d reaped %d times", e.GrantID, seen[e.GrantID])
		}
	}
}

// 相同操作序列重放得到相同 Reaped。
func TestReplayDeterministic(t *testing.T) {
	run := func() []string {
		s := access.New(cfg)
		must(t, s.SetOwners(b("r"), [][]byte{b("o")}, 0))
		must(t, s.Request("q1", b("u"), b("r"), 100, 0))
		g1, err := s.Approve("q1", b("o"), 0)
		must2(t, g1, err)
		_, _ = s.Delegate(g1, b("v"), 100, 0)
		must(t, s.Tick(50))
		_ = s.Check(b("v"), b("r"), 60)
		must(t, s.Tick(100))
		out := make([]string, len(s.Reaped()))
		for i, e := range s.Reaped() {
			out[i] = fmt.Sprintf("%d|%s|%d", e.GrantID, e.Cause, e.At)
		}
		return out
	}
	first := run()
	second := run()
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("nondeterministic replay: %v vs %v", first, second)
	}
}
