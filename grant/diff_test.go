package grant

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/ledger"
	"ontology/policy"
)

// 与朴素模拟逐步对照：同一操作序列同时作用于 Service 与 sim，
// 每步比较错误结果与若干历史/当前时刻的 Access 答案。
func TestNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	p0 := policy.Policy{Dmax: 50, Rw: 20, Lk: 15, Cmax: 3}
	svc, err := NewService(p0)
	if err != nil {
		t.Fatal(err)
	}
	sm := newSim(p0)
	regs := []struct {
		n  string
		r  policy.Role
		sc []string
	}{
		{"alice", policy.RoleEngineer, nil},
		{"bob", policy.RoleEngineer, nil},
		{"m1", policy.RoleManager, []string{"/db"}},
		{"sec", policy.RoleSecurity, nil},
	}
	for _, r := range regs {
		if e1, e2 := svc.Register(0, r.n, r.r, r.sc), sm.register(0, r.n, r.r, r.sc); e1 != e2 {
			t.Fatalf("register %s 分歧: %v vs %v", r.n, e1, e2)
		}
	}
	reqNames := []string{"alice", "bob", "ghost"}
	approvers := []string{"m1", "sec", "alice", "ghost"}
	resources := []string{"/db/a", "/db/b", "/app"}
	var ids []string
	for step := 0; step < 300; step++ {
		now := sm.clock + int64(rng.Intn(6))
		var e1, e2 error
		var desc string
		switch rng.Intn(4) {
		case 0:
			id := fmt.Sprintf("g%d", step)
			if len(ids) > 0 && rng.Intn(3) == 0 {
				id = ids[rng.Intn(len(ids))] // 制造 id 重复
			}
			req := reqNames[rng.Intn(len(reqNames))]
			res := resources[rng.Intn(len(resources))]
			d := int64(rng.Intn(60))
			desc = fmt.Sprintf("Request(now=%d,id=%s,req=%s,res=%s,D=%d)", now, id, req, res, d)
			e1, e2 = svc.Request(now, id, req, res, d), sm.request(now, id, req, res, d)
			if e1 == nil {
				ids = append(ids, id)
			}
		case 1, 2:
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.Intn(len(ids))]
			ap := approvers[rng.Intn(len(approvers))]
			if rng.Intn(2) == 0 {
				desc = fmt.Sprintf("Approve(now=%d,id=%s,by=%s)", now, id, ap)
				e1, e2 = svc.Approve(now, id, ap), sm.review(now, id, ap, true)
			} else {
				desc = fmt.Sprintf("Reject(now=%d,id=%s,by=%s)", now, id, ap)
				e1, e2 = svc.Reject(now, id, ap), sm.review(now, id, ap, false)
			}
		case 3:
			np := policy.Policy{Dmax: 1 + int64(rng.Intn(80)), Rw: 1 + int64(rng.Intn(40)),
				Lk: int64(rng.Intn(30)), Cmax: 1 + int64(rng.Intn(4))}
			desc = fmt.Sprintf("SetPolicy(now=%d,p=%+v)", now, np)
			e1, e2 = svc.SetPolicy(now, np), sm.setPolicy(now, np)
		}
		t.Logf("step %d: %s -> svc=%v sim=%v", step, desc, e1, e2)
		if e1 != e2 {
			t.Fatalf("step %d 分歧: svc=%v sim=%v", step, e1, e2)
		}
		clk := sm.clock // 被拒绝的操作不推进时钟，探针不得越过时钟
		for _, tp := range []int64{0, clk / 2, clk} {
			for _, who := range []string{"alice", "bob"} {
				for _, res := range resources {
					got, err := svc.Access(who, res, tp)
					if err != nil {
						t.Fatalf("step %d Access err: %v", step, err)
					}
					if want := sm.access(who, res, tp); got != want {
						t.Fatalf("step %d Access(%s,%s,%d): svc=%v sim=%v",
							step, who, res, tp, got, want)
					}
				}
			}
		}
		for id, g := range svc.grants {
			if !(g.Start <= g.EffEnd() && g.EffEnd() <= g.NominalEnd) {
				t.Fatalf("step %d: %s 违反 start<=effEnd<=nominalEnd", step, id)
			}
		}
	}
	t.Logf("模拟完成: 300 步, 已接受授权 %d 个, 账本 %d 条", len(ids), svc.Ledger(0)[len(svc.Ledger(0))-1].Seq)
}

// Access 考察的授权数只与自己有关：其他主体 1 与 5000 两档下，
// 非导出计数器 accessChecks 的增量相同。
func TestAccessChecksCounter(t *testing.T) {
	for _, other := range []int{1, 5000} {
		svc, err := NewService(policy.Policy{Dmax: 1e9, Rw: 1e9, Lk: 0, Cmax: 1e9})
		if err != nil {
			t.Fatal(err)
		}
		svc.Register(0, "me", policy.RoleEngineer, nil)
		svc.Register(0, "other", policy.RoleEngineer, nil)
		svc.Request(0, "mine", "me", "/x", 10)
		for i := 0; i < other; i++ {
			if err := svc.Request(0, fmt.Sprintf("o%d", i), "other", "/y", 10); err != nil {
				t.Fatal(err)
			}
		}
		before := svc.accessChecks
		if _, err := svc.Access("me", "/z", 0); err != nil { // 不命中，扫描全部自有授权
			t.Fatal(err)
		}
		got := svc.accessChecks - before
		t.Logf("其他主体授权数=%d, Access(me) 考察授权数=%d", other, got)
		if got != 1 {
			t.Fatalf("other=%d 时考察数=%d, want 1(只扫自己的授权)", other, got)
		}
	}
}

// 相同操作序列重放得到逐条相同的账本。
func TestReplayDeterministic(t *testing.T) {
	run := func() []ledger.Entry {
		svc := newExampleSvc(t)
		svc.Request(10, "g1", "alice", "/db/prod", 100)
		svc.SetPolicy(20, policy.Policy{Dmax: 100, Rw: 10, Lk: 50, Cmax: 3})
		svc.Request(21, "g2", "alice", "/db/other", 50)
		svc.Approve(30, "g1", "m1")
		svc.Reject(31, "g2", "sec")
		return svc.Ledger(0)
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("账本长度不一致: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("条目 %d 不一致: %+v vs %+v", i, a[i], b[i])
		}
	}
	t.Logf("重放一致: %d 条, 末条=%+v", len(a), a[len(a)-1])
}

// 并发冒烟：多 goroutine 混合调用，-race 下无数据竞争且 Seq 连续。
func TestConcurrentSmoke(t *testing.T) {
	svc := newExampleSvc(t)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				now := int64(i)
				svc.Request(now, fmt.Sprintf("w%dg%d", w, i), "alice", "/db/prod", 5)
				svc.Approve(now, fmt.Sprintf("w%dg%d", w, i), "sec")
				svc.Access("alice", "/db/prod", now)
				svc.Ledger(0)
			}
		}(w)
	}
	wg.Wait()
	for i, e := range svc.Ledger(0) {
		if e.Seq != int64(i+1) {
			t.Fatalf("并发后 Seq 有洞: [%d].Seq=%d", i, e.Seq)
		}
	}
}
