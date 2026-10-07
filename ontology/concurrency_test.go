package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestDeleteCreateRace 验证「删除释放后立即被并发创建占用」：
// 目标 Article 的终点侧基数恰好一，初始已有一条链接占满名额。
// 大量并发删除/创建交错，任何时刻终点占用都不得超过 1。
func TestDeleteCreateRace(t *testing.T) {
	env := setup(t)
	l := env.ledger
	must(t, l.CreateLink("authored", "p1", "a1"))

	const workers = 16
	const rounds = 200
	var wg sync.WaitGroup
	var violations []string
	var muViol sync.Mutex

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				// 删除者与创建者争抢同一个「恰好一」名额。
				if (id+r)%2 == 0 {
					_ = l.DeleteLink("authored", "p1", "a1")
				} else {
					// 若名额恰好空出，此创建应成功；若仍被占，应被终点侧拒绝。
					err := l.CreateLink("authored", "p2", "a1")
					// 另一种同样合法的串行结果：上一轮创建的 p2->a1 尚未被删除，
					// 此时命中「有序对已存在」（参数非法）。二者都等价于某种串行顺序。
					if err != nil {
						if r := reasonOf(err); r != ReasonTargetCardinality && r != ReasonInvalidArgument {
							muViol.Lock()
							violations = append(violations, fmt.Sprintf("unexpected err: %v", err))
							muViol.Unlock()
						}
					}
				}
				if occ := l.Occupied("authored", SideTarget, "a1"); occ < 0 || occ > 1 {
					muViol.Lock()
					violations = append(violations, fmt.Sprintf("a1 occupancy out of [0,1]: %d", occ))
					muViol.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()

	// 最终不变式：a1 的终点计数与实际链接数一致，且不超过 1。
	occ := l.Occupied("authored", SideTarget, "a1")
	count := 0
	for k := range l.Snapshot() {
		if k.Target == "a1" {
			count++
		}
	}
	t.Logf("delete/create race done: final a1 occupancy=%d actual-links-to-a1=%d violations=%d",
		occ, count, len(violations))
	if len(violations) > 0 {
		t.Fatalf("serializability violations: %v", violations[:min(5, len(violations))])
	}
	if occ != count || occ > 1 {
		t.Fatalf("ledger count drifted from links: occ=%d links=%d", occ, count)
	}
}

// TestConcurrentBatchesNeverExceedBounds 用「尚有一个名额」的经典陷阱压测：
// 起点 p9 侧上限 1，大量并发 goroutine 同时尝试不同链接，至多 1 个成功。
func TestConcurrentBatchesNeverExceedBounds(t *testing.T) {
	l := NewLedger()
	imp := NewImporter(l)
	must(t, l.RegisterLinkType(LinkTypeSpec{
		Name:        "one",
		SourceType:  "S",
		TargetType:  "T",
		SourceBound: ExactlyOne(),
		TargetBound: Unlimited(),
	}))
	must(t, l.CreateObject("S", "p9"))
	for i := 0; i < 64; i++ {
		must(t, l.CreateObject("T", InstanceID(fmt.Sprintf("t%d", i))))
	}

	const n = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	successes := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			pairs := []InstancePair{{Source: "p9", Target: InstanceID(fmt.Sprintf("t%d", i))}}
			rep := imp.Import("one", pairs, ModeBestEffort)
			successes <- (rep.Results[0].Status == StatusAccepted)
		}(i)
	}
	close(start)
	wg.Wait()
	close(successes)

	accepted := 0
	for s := range successes {
		if s {
			accepted++
		}
	}
	occ := l.Occupied("one", SideSource, "p9")
	t.Logf("contended single-slot: accepted=%d p9 occupancy=%d links=%d",
		accepted, occ, l.LinkCount())
	if accepted != 1 || occ != 1 || l.LinkCount() != 1 {
		t.Fatalf("lost-update race: accepted=%d occ=%d links=%d", accepted, occ, l.LinkCount())
	}
}

// TestDeterministicReplay 重放同一操作序列两次，链接集合与计数必须完全相同。
func TestDeterministicReplay(t *testing.T) {
	script := []string{"c p1 a1", "c p1 a2", "c p1 a3", "d p1 a1", "c p2 a1", "c p3 a2"}

	run := func() (map[LinkKey]struct{}, map[countKey]int) {
		env := setup(t)
		for _, op := range script {
			var cmd, s, d2 string
			if _, err := fmt.Sscanf(op, "%s %s %s", &cmd, &s, &d2); err != nil {
				t.Fatal(err)
			}
			if cmd == "c" {
				_ = env.ledger.CreateLink("authored", InstanceID(s), InstanceID(d2))
			} else {
				_ = env.ledger.DeleteLink("authored", InstanceID(s), InstanceID(d2))
			}
		}
		env.ledger.mu.Lock()
		counts := make(map[countKey]int, len(env.ledger.counts))
		for k, v := range env.ledger.counts {
			counts[k] = v
		}
		env.ledger.mu.Unlock()
		return env.ledger.Snapshot(), counts
	}

	links1, counts1 := run()
	links2, counts2 := run()
	t.Logf("replay script=%v links=%d count-entries=%d", script, len(links1), len(counts1))
	if !sameKeySet(links1, links2) || !sameCounts(counts1, counts2) {
		t.Fatal("replay produced different state")
	}
}

func sameKeySet(a, b map[LinkKey]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func sameCounts(a, b map[countKey]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
