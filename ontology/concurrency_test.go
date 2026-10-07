package ontology

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrentDecreaseAndCreate 并发执行基数下调/上调与新建请求，
// 然后把管理器实际生效的操作全序（opLog）重放到独立的朴素串行实现上，
// 对照每个请求的接受/拒绝结果与最终待处理集合是否完全一致。
// 这验证了并发结果等价于按某个全序串行执行。
func TestConcurrentDecreaseAndCreate(t *testing.T) {
	m := NewManager()
	const workers = 8
	const opsPerWorker = 60
	if err := m.DefineLinkType("T", "A", "B", 10, Unlimited); err != nil {
		t.Fatal(err)
	}
	m.RegisterObject("root")
	for i := 0; i < workers*opsPerWorker; i++ {
		m.RegisterObject(fmt.Sprintf("t%d", i))
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w) * 7919))
			for i := 0; i < opsPerWorker; i++ {
				if rng.Intn(3) == 0 {
					// 1/3 的操作是限额调整（含下调与上调）。
					_ = m.SetLimit("T", DirectionOut, 2+rng.Intn(15))
				} else {
					n := w*opsPerWorker + i
					_ = m.CreateLink("T", fmt.Sprintf("l%d", n), "root", fmt.Sprintf("t%d", n))
				}
			}
		}(w)
	}
	wg.Wait()

	// 按实际生效全序重放到朴素串行实现。
	naive := newNaiveCore(10, Unlimited)
	ops := m.appliedOps()
	if len(ops) == 0 {
		t.Fatal("no ops recorded")
	}
	for i, op := range ops {
		switch op.op {
		case "create":
			got := naive.create(op.linkID, op.source, op.target)
			if got != op.accepted {
				t.Fatalf("op %d create %s: manager accepted=%v, naive=%v", i, op.linkID, op.accepted, got)
			}
		case "setlimit":
			naive.setLimit(op.dir, op.limit)
		}
	}
	// 对照最终待处理集合。
	wantPending := naive.pendingLinks()
	gotPending := make(map[string]bool)
	for id := range m.links {
		if m.isPendingLocked(m.links[id]) {
			gotPending[id] = true
		}
	}
	if len(gotPending) != len(wantPending) {
		t.Fatalf("pending count: manager=%d naive=%d", len(gotPending), len(wantPending))
	}
	for id := range wantPending {
		if !gotPending[id] {
			t.Fatalf("link %s pending in naive but not in manager", id)
		}
	}
	// 不变量：每个登记组的有效链接数不超过有效上限。
	for gk, g := range m.groups {
		lt := m.types[gk.typeID]
		eff := m.effectiveLimit(lt, g, gk.dir)
		active := len(g.links) - len(g.pendingList)
		if eff != Unlimited && active > eff {
			t.Fatalf("group %+v: active=%d exceeds effective limit %d", gk, active, eff)
		}
	}
}

// TestSerialEquivalenceRepeated 同一串操作在管理器与朴素实现上
// 顺序执行，结果必须一致（确定性复核）。
func TestSerialEquivalenceRepeated(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	m := NewManager()
	if err := m.DefineLinkType("T", "A", "B", 12, Unlimited); err != nil {
		t.Fatal(err)
	}
	m.RegisterObject("root")
	naive := newNaiveCore(12, Unlimited)
	for i := 0; i < 300; i++ {
		if rng.Intn(4) == 0 {
			lim := 1 + rng.Intn(20)
			if err := m.SetLimit("T", DirectionOut, lim); err != nil {
				t.Fatal(err)
			}
			naive.setLimit(DirectionOut, lim)
			continue
		}
		obj := fmt.Sprintf("t%d", i)
		m.RegisterObject(obj)
		id := fmt.Sprintf("l%d", i)
		err := m.CreateLink("T", id, "root", obj)
		gotAccept := err == nil
		wantAccept := naive.create(id, "root", obj)
		if gotAccept != wantAccept {
			t.Fatalf("op %d: manager accepted=%v, naive=%v", i, gotAccept, wantAccept)
		}
	}
	wantPending := naive.pendingLinks()
	for id, l := range m.links {
		got := m.isPendingLocked(l)
		if got != wantPending[id] {
			t.Fatalf("link %s: manager pending=%v, naive=%v", id, got, wantPending[id])
		}
	}
}
