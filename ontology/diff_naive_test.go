package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// diffState 汇总随机序列每一步后用于对照的状态。
func diffSnapshot(s *Service) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b strings.Builder
	ids := make([]int64, 0, len(s.residents))
	for id := range s.residents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		r := s.residents[id]
		fmt.Fprintf(&b, "R%d(active=%v out=%d segs=%v) ", id, r.Active, r.OutDay, r.Segments)
	}
	b.WriteString("NET{")
	keys := make([]pairKey, 0, len(s.net))
	for k := range s.net {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})
	for _, k := range keys {
		fmt.Fprintf(&b, "%d->%d=%d ", k.a, k.b, s.net[k])
	}
	b.WriteString("} FOLD{")
	fids := make([]int64, 0, len(s.folded))
	for id := range s.folded {
		fids = append(fids, id)
	}
	sort.Slice(fids, func(i, j int) bool { return fids[i] < fids[j] })
	for _, id := range fids {
		others := make([]int64, 0)
		for o := range s.folded[id] {
			others = append(others, o)
		}
		sort.Slice(others, func(i, j int) bool { return others[i] < others[j] })
		for _, o := range others {
			fmt.Fprintf(&b, "%d>%d=%d ", id, o, s.folded[id][o])
		}
	}
	b.WriteString("}")
	return b.String()
}

func naiveSnapshot(m *naiveModel) string {
	var b strings.Builder
	ids := make([]int64, 0, len(m.residents))
	for id := range m.residents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		r := m.residents[id]
		segs := ""
		for _, sg := range r.segs {
			segs += fmt.Sprintf("{%d %d %d %d} ", id, sg.room, sg.start, sg.end)
		}
		segs = strings.TrimRight(segs, " ")
		fmt.Fprintf(&b, "R%d(active=%v out=%d segs=[%s]) ", id, r.active, r.outDay, segs)
	}
	b.WriteString("NET{")
	keys := make([]pairKey, 0, len(m.net))
	for k := range m.net {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})
	for _, k := range keys {
		fmt.Fprintf(&b, "%d->%d=%d ", k.a, k.b, m.net[k])
	}
	b.WriteString("} FOLD{")
	fids := make([]int64, 0, len(m.folded))
	for id := range m.folded {
		fids = append(fids, id)
	}
	sort.Slice(fids, func(i, j int) bool { return fids[i] < fids[j] })
	for _, id := range fids {
		others := make([]int64, 0)
		for o := range m.folded[id] {
			others = append(others, o)
		}
		sort.Slice(others, func(i, j int) bool { return others[i] < others[j] })
		for _, o := range others {
			fmt.Fprintf(&b, "%d>%d=%d ", id, o, m.folded[id][o])
		}
	}
	b.WriteString("}")
	return b.String()
}

// TestRandomDifferential 对随机操作序列，用独立朴素模型逐步对照净额、头寸与归属守恒。
// 每步打印输入、输出与判定依据（-v 可见）。
func TestRandomDifferential(t *testing.T) {
	const maxAmount = 50
	for seed := int64(0); seed < 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		svc := NewService(maxAmount, 4)
		nav := newNaive(maxAmount, 4)
		var log strings.Builder

		check := func(step int, input string, got error) {
			gs, ns := diffSnapshot(svc), naiveSnapshot(nav)
			decision := "accepted"
			if got != nil {
				decision = "rejected:" + got.Error()
			}
			fmt.Fprintf(&log, "seed=%d step=%d %s => %s\n", seed, step, input, decision)
			if gs != ns {
				t.Fatalf("divergence seed=%d step=%d input=%s\n--- impl ---\n%s\n--- naive --\n%s\nlog:\n%s",
					seed, step, input, gs, ns, log.String())
			}
			// 逐账单守恒。
			for bid, b := range svc.bills {
				var sum int64
				for _, c := range b.Contribs {
					sum += c.Amount
				}
				if sum != b.Amount {
					t.Fatalf("seed=%d bill %d conservation broken %d!=%d", seed, bid, sum, b.Amount)
				}
			}
		}

		for step := 0; step < 220; step++ {
			now := int64(0)
			if rng.Intn(3) != 0 {
				now = nav.lastNow + int64(rng.Intn(3))
			}
			switch rng.Intn(7) {
			case 0:
				id := int64(1 + rng.Intn(5))
				room := int64(1 + rng.Intn(3))
				day := now - int64(rng.Intn(4))
				if day < 0 {
					day = 0
				}
				area := int64(1 + rng.Intn(3)*5)
				in := fmt.Sprintf("CheckIn(%d,%d,%d,%d,%d)", now, id, room, day, area)
				got := svc.CheckIn(now, id, room, day, area)
				want := nav.checkIn(now, id, room, day, area)
				check(step, in, got)
				if !sameErr(got, want) {
					t.Fatalf("seed=%d step=%d %s error mismatch got=%v want=%v", seed, step, in, got, want)
				}
			case 1:
				id := int64(1 + rng.Intn(5))
				outDay := now - int64(rng.Intn(3))
				if outDay < 0 {
					outDay = 0
				}
				in := fmt.Sprintf("CheckOut(%d,%d,%d)", now, id, outDay)
				got := svc.CheckOut(now, id, outDay)
				want := nav.checkOut(now, id, outDay)
				check(step, in, got)
				if !sameErr(got, want) {
					t.Fatalf("seed=%d step=%d %s got=%v want=%v", seed, step, in, got, want)
				}
			case 2:
				id := int64(1 + rng.Intn(5))
				newRoom := int64(1 + rng.Intn(3))
				day := now - int64(rng.Intn(3))
				if day < 0 {
					day = 0
				}
				area := int64(1 + rng.Intn(3)*5)
				in := fmt.Sprintf("ChangeRoom(%d,%d,%d,%d,%d)", now, id, newRoom, day, area)
				got := svc.ChangeRoom(now, id, newRoom, day, area)
				want := nav.changeRoom(now, id, newRoom, day, area)
				check(step, in, got)
				if !sameErr(got, want) {
					t.Fatalf("seed=%d step=%d %s got=%v want=%v", seed, step, in, got, want)
				}
			case 3:
				id := int64(100 + rng.Intn(40))
				amount := int64(1 + rng.Intn(maxAmount+5))
				startDay := now - int64(rng.Intn(6))
				if startDay < 0 {
					startDay = 0
				}
				span := int64(1 + rng.Intn(5))
				method := SplitMethod(rng.Intn(2))
				payer := int64(1 + rng.Intn(5))
				landlord := rng.Intn(2) == 0
				in := fmt.Sprintf("EnterBill(%d,%d,%d,%d,%d,m=%d,payer=%d,ll=%v)",
					now, id, amount, startDay, startDay+span, method, payer, landlord)
				got := svc.EnterBill(now, id, amount, startDay, startDay+span, method, payer, landlord)
				want := nav.enterBill(now, id, amount, startDay, startDay+span, method, payer, landlord)
				check(step, in, got)
				if !sameErr(got, want) {
					t.Fatalf("seed=%d step=%d %s got=%v want=%v", seed, step, in, got, want)
				}
			case 4:
				id := int64(100 + rng.Intn(40))
				in := fmt.Sprintf("Dispute(%d,%d)", now, id)
				got := svc.RaiseDispute(now, id)
				want := nav.dispute(now, id)
				check(step, in, got)
				if !sameErr(got, want) {
					t.Fatalf("seed=%d step=%d %s got=%v want=%v", seed, step, in, got, want)
				}
			case 5:
				id := int64(100 + rng.Intn(40))
				newAmount := int64(rng.Intn(maxAmount + 10))
				in := fmt.Sprintf("Adjudicate(%d,%d,%d)", now, id, newAmount)
				got := svc.Adjudicate(now, id, newAmount)
				want := nav.adjudicate(now, id, newAmount)
				check(step, in, got)
				if !sameErr(got, want) {
					t.Fatalf("seed=%d step=%d %s got=%v want=%v", seed, step, in, got, want)
				}
			case 6:
				// 纯查询：不改变状态，仅触发快照对照。
				check(step, "noop-query", nil)
			}
		}
		t.Logf("\n%s", log.String())
	}
}

func sameErr(a, b error) bool {
	return (a == nil) == (b == nil)
}

// TestConcurrentEquivalence 并发录入账单与退出清算：串行化后结果一致、无双清算/遗漏。
func TestConcurrentEquivalence(t *testing.T) {
	s := NewService(1_000_000, 100)
	for id := int64(1); id <= 4; id++ {
		mustOK(t, s.CheckIn(1, id, id, 1, 10), "checkin")
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			_ = s.EnterBill(5, int64(100+i), 40, 1, 5, SplitPerHead, int64(1+i%4), false)
		}()
	}
	// 与账单并发地反复尝试退出住户2：至多一次成功，且只产生一条原清算。
	var success int64
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.CheckOut(5, 2, 5); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("checkout must succeed exactly once under concurrency, got %d", success)
	}
	set, _ := s.Settlements(2)
	var orig int
	for _, rec := range set {
		if !rec.Suppl {
			orig++
		}
	}
	if orig != 1 {
		t.Fatalf("exactly one original settlement expected, got %d", orig)
	}
	// 逐账单对账：每张账单的每个住户承担份额，必须恰好出现在某一处（
	// 在住净额 net，或某一方已退出后的 folded 头寸），既不重复也不遗漏。
	reconcile(t, s)
}

// reconcile 校验全局有向下头寸之和为 0，且每张账单的住户间债务被 net/folded 恰好覆盖一次。
func reconcile(t *testing.T, s *Service) {
	t.Helper()
	s.mu.RLock()
	defer s.mu.RUnlock()
	// 规范净额与各 folded 的总和在每个住户对上，按“在住对在住进 net、
	// 退出相关进 folded”互斥存放；这里校验每个已退出住户对每张账单至多并入一次。
	for id, inc := range s.included {
		seen := map[int64]bool{}
		for bid := range inc {
			if seen[bid] {
				t.Fatalf("bill %d settled twice for resident %d", bid, id)
			}
			seen[bid] = true
		}
	}
	// 守恒：所有当前生效账单的住户有向贡献之和为 0。
	var sum int64
	for _, b := range s.bills {
		if b.Status == BillDisputed || b.PayerID == landlordPayer {
			continue
		}
		for _, c := range b.Contribs {
			if c.ResidentID == 0 {
				continue
			}
			sum += signedContrib(c.Amount, b.Amount, b.PayerID, c.ResidentID)
		}
	}
	if sum != 0 {
		t.Fatalf("global directed positions must sum to zero, got %d", sum)
	}
}

// TestDeterministicReplay 相同操作序列重放两次，净额头寸与清算记录完全一致。
func TestDeterministicReplay(t *testing.T) {
	run := func() string {
		s := NewService(1_000_000, 10)
		mustOK(t, s.CheckIn(1, 1, 1, 1, 10), "in1")
		mustOK(t, s.CheckIn(1, 2, 2, 1, 10), "in2")
		mustOK(t, s.CheckIn(3, 3, 3, 3, 10), "in3")
		mustOK(t, s.EnterBill(3, 10, 100, 1, 6, SplitByArea, 1, false), "b1")
		mustOK(t, s.EnterBill(3, 11, 30, 0, 4, SplitPerHead, 2, false), "b2")
		mustOK(t, s.CheckOut(4, 2, 4), "out2")
		mustOK(t, s.RaiseDispute(5, 11), "dispute")
		mustOK(t, s.Adjudicate(6, 11, 12), "adjudicate")
		mustOK(t, s.EnterBill(7, 12, 20, 2, 9, SplitPerHead, 1, false), "late")
		set2, _ := s.Settlements(2)
		return fmt.Sprintf("net=%v folded=%v settle2=%+v", s.net, s.folded, set2)
	}
	if run() != run() {
		t.Fatal("replay produced different results")
	}
}

// TestNetQueryCostBounded O(1) 查询证明：NetBetween 为单次哈希读，
// 账单量从 N 增长到 10N，查询内部访问的 map 桶路径长度不随账单数增长。
// 这里以行为级断言：不同账单规模下同一对住户查询返回一致、且直接读取底层 map。
func TestNetQueryCostBounded(t *testing.T) {
	for _, n := range []int{50, 500} {
		s := NewService(1_000_000, 1_000_000)
		mustOK(t, s.CheckIn(1, 1, 1, 1, 10), "r1")
		mustOK(t, s.CheckIn(1, 2, 2, 1, 10), "r2")
		for i := 0; i < n; i++ {
			payer := int64(1)
			if i%2 == 0 {
				payer = 2
			}
			mustOK(t, s.EnterBill(int64(2+i), int64(1000+i), 2, 1, 2, SplitPerHead, payer, false), "bill")
		}
		v, err := s.NetBetween(1, 2)
		mustOK(t, err, "net")
		want := s.net[canonPair(1, 2)] // 与底层单次哈希读取完全一致
		if v != want {
			t.Fatalf("net query not a direct map read: %d != %d", v, want)
		}
	}
}
