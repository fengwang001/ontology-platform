package wave_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/slot"
	"ontology/wave"
)

type locSpec struct {
	id   string
	kind slot.Kind
	sku  string
	qty  int64
}

type orderSpec struct {
	id       string
	priority int
	lines    []wave.Line
}

type testWorld struct {
	pallets map[string]int64
	locs    []locSpec
	orders  []orderSpec
}

func buildWorld(t *testing.T, w testWorld) *wave.Coordinator {
	t.Helper()
	c := wave.New()
	for sku, p := range w.pallets {
		must(t, c.SetPallet(sku, p))
	}
	for _, l := range w.locs {
		must(t, c.PutStock(l.id, l.kind, l.sku, l.qty))
	}
	for _, o := range w.orders {
		must(t, c.AddOrder(o.id, o.priority, o.lines))
	}
	return c
}

func allocsOf(c *wave.Coordinator, order string) map[string]int64 {
	rows, _ := c.OrderAllocs(order)
	m := map[string]int64{}
	for _, a := range rows {
		m[a.Loc] = a.Qty
	}
	return m
}

func eqMap(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

func TestAllocationBulkShortfallToPick(t *testing.T) {
	c := buildWorld(t, testWorld{
		pallets: map[string]int64{"x": 10},
		locs: []locSpec{
			{"B1", slot.Bulk, "x", 12},
			{"K1", slot.Pick, "x", 8},
		},
	})
	must(t, c.AddOrder("O", 0, []wave.Line{{"x", 20}}))
	res, err := c.Release([]string{"O"})
	must(t, err)
	if res[0].State != wave.StatusAllocated {
		t.Fatal(res[0].Err)
	}
	if got := allocsOf(c, "O"); !eqMap(got, map[string]int64{"B1": 12, "K1": 8}) {
		t.Fatalf("got %v", got)
	}
}

func TestBackfillTieBreakByLoc(t *testing.T) {
	c := buildWorld(t, testWorld{
		pallets: map[string]int64{"x": 10},
		locs: []locSpec{
			{"B1", slot.Bulk, "x", 6},
			{"B2", slot.Bulk, "x", 6},
		},
	})
	must(t, c.AddOrder("O", 0, []wave.Line{{"x", 9}}))
	res, err := c.Release([]string{"O"})
	must(t, err)
	if res[0].State != wave.StatusAllocated {
		t.Fatal(res[0].Err)
	}
	if got := allocsOf(c, "O"); !eqMap(got, map[string]int64{"B1": 6, "B2": 3}) {
		t.Fatalf("got %v", got)
	}
}

func TestExactMultipleSkipsPick(t *testing.T) {
	c := buildWorld(t, testWorld{
		pallets: map[string]int64{"x": 10},
		locs: []locSpec{
			{"B1", slot.Bulk, "x", 20},
			{"K1", slot.Pick, "x", 9},
		},
	})
	must(t, c.AddOrder("O", 0, []wave.Line{{"x", 20}}))
	_, err := c.Release([]string{"O"})
	must(t, err)
	if got := allocsOf(c, "O"); !eqMap(got, map[string]int64{"B1": 20}) {
		t.Fatalf("got %v", got)
	}
}

func TestBackorderRollbackBenefitsNext(t *testing.T) {
	c := buildWorld(t, testWorld{
		pallets: map[string]int64{"x": 10, "y": 10},
		locs:    []locSpec{{"B1", slot.Bulk, "x", 10}},
	})
	must(t, c.AddOrder("O1", 9, []wave.Line{{"x", 10}, {"y", 10}}))
	must(t, c.AddOrder("O2", 0, []wave.Line{{"x", 10}}))
	res, err := c.Release([]string{"O1", "O2"})
	must(t, err)
	if res[0].Order != "O1" || res[0].State != wave.StatusBackorder {
		t.Fatalf("res0=%+v", res[0])
	}
	if res[1].Order != "O2" || res[1].State != wave.StatusAllocated {
		t.Fatalf("res1=%+v", res[1])
	}
	if got := allocsOf(c, "O2"); !eqMap(got, map[string]int64{"B1": 10}) {
		t.Fatalf("O2 got %v", got)
	}
	l, _ := c.LocView("B1")
	if l.OnHand != 10 || l.Reserved != 10 {
		t.Fatalf("B1 onHand=%d reserved=%d", l.OnHand, l.Reserved)
	}
}

func TestReleaseSortingAndBatchReject(t *testing.T) {
	c := buildWorld(t, testWorld{
		pallets: map[string]int64{"x": 100},
		locs:    []locSpec{{"B1", slot.Bulk, "x", 5}},
	})
	must(t, c.AddOrder("a", 5, []wave.Line{{"x", 2}}))
	must(t, c.AddOrder("b", 5, []wave.Line{{"x", 2}}))
	must(t, c.AddOrder("c", 9, []wave.Line{{"x", 2}}))
	res, err := c.Release([]string{"b", "a", "c"})
	must(t, err)
	if res[0].Order != "c" || res[1].Order != "a" || res[2].Order != "b" {
		t.Fatalf("order=%v,%v,%v", res[0].Order, res[1].Order, res[2])
	}
	if res[2].State != wave.StatusBackorder {
		t.Fatalf("b should backorder: %+v", res[2])
	}
	if _, err := c.Release([]string{"b", "b"}); !errors.Is(err, wave.ErrInvalidArg) {
		t.Fatalf("dup ids got %v", err)
	}
	if _, err := c.Release([]string{"b", "ghost"}); !errors.Is(err, wave.ErrNotFound) {
		t.Fatalf("missing got %v", err)
	}
	if _, err := c.Release([]string{"b", "a"}); !errors.Is(err, wave.ErrBadState) {
		t.Fatalf("state got %v", err)
	}
	st, _, _ := c.Status("b")
	if st != wave.StatusBackorder {
		t.Fatalf("b=%v", st)
	}
}

func TestPickDone(t *testing.T) {
	c := buildWorld(t, testWorld{
		pallets: map[string]int64{"x": 10},
		locs:    []locSpec{{"K1", slot.Pick, "x", 5}},
	})
	must(t, c.AddOrder("O", 0, []wave.Line{{"x", 5}}))
	_, err := c.Release([]string{"O"})
	must(t, err)
	if err := c.Pick("O", "K1", 3); !errors.Is(err, wave.ErrQtyMismatch) {
		t.Fatalf("partial pick got %v", err)
	}
	must(t, c.Pick("O", "K1", 5))
	st, short, err := c.Status("O")
	must(t, err)
	if st != wave.StatusDone || short != 0 {
		t.Fatalf("state=%v short=%d", st, short)
	}
	l, _ := c.LocView("K1")
	if l.OnHand != 0 || l.Reserved != 0 {
		t.Fatalf("K1 onHand=%d reserved=%d", l.OnHand, l.Reserved)
	}
}

func TestErrorOrdering(t *testing.T) {
	world := testWorld{
		pallets: map[string]int64{"x": 10},
		locs: []locSpec{
			{"B1", slot.Bulk, "x", 10},
			{"K1", slot.Pick, "x", 5},
		},
		orders: []orderSpec{{"O1", 0, []wave.Line{{"x", 5}}}},
	}

	t.Run("invalid", func(t *testing.T) {
		c := buildWorld(t, world)
		many := make([]wave.Line, 51)
		for i := range many {
			many[i] = wave.Line{"x", 1}
		}
		checks := []error{
			c.SetPallet("", 10),
			c.SetPallet("zz", 0),
			c.SetPallet("zz", 1_000_001),
			c.PutStock("", slot.Bulk, "ghost", 1),
			c.PutStock("L", slot.Kind(9), "x", 1),
			c.PutStock("L", slot.Bulk, "x", 0),
			c.AddOrder("", 0, []wave.Line{{"x", 1}}),
			c.AddOrder("z", 10, []wave.Line{{"x", 1}}),
			c.AddOrder("z", 0, nil),
			c.AddOrder("z", 0, many),
			c.AddOrder("z", 0, []wave.Line{{"x", 1}, {"x", 2}}),
		}
		for i, err := range checks {
			if !errors.Is(err, wave.ErrInvalidArg) {
				t.Fatalf("check %d got %v", i, err)
			}
		}
		if _, err := c.Release(nil); !errors.Is(err, wave.ErrInvalidArg) {
			t.Fatalf("release nil got %v", err)
		}
		if err := c.Pick("", "nope", 0); !errors.Is(err, wave.ErrInvalidArg) {
			t.Fatalf("pick got %v", err)
		}
		if err := c.ShortPick("", "nope", -1); !errors.Is(err, wave.ErrInvalidArg) {
			t.Fatalf("short got %v", err)
		}
		if err := c.Unlock("", -1); !errors.Is(err, wave.ErrInvalidArg) {
			t.Fatalf("unlock got %v", err)
		}
	})

	t.Run("notfound", func(t *testing.T) {
		c := buildWorld(t, world)
		if err := c.PutStock("L9", slot.Bulk, "ghost", 1); !errors.Is(err, wave.ErrNotFound) {
			t.Fatalf("put unknown sku got %v", err)
		}
		if _, err := c.Release([]string{"ghost"}); !errors.Is(err, wave.ErrNotFound) {
			t.Fatalf("release got %v", err)
		}
		if err := c.Pick("ghost", "B1", 1); !errors.Is(err, wave.ErrNotFound) {
			t.Fatalf("pick order got %v", err)
		}
		if err := c.Pick("O1", "ghost", 1); !errors.Is(err, wave.ErrNotFound) {
			t.Fatalf("pick loc got %v", err)
		}
		if err := c.ShortPick("O1", "ghost", 0); !errors.Is(err, wave.ErrNotFound) {
			t.Fatalf("short loc got %v", err)
		}
		if err := c.Unlock("ghost", 0); !errors.Is(err, wave.ErrNotFound) {
			t.Fatalf("unlock got %v", err)
		}
	})

	t.Run("badstate", func(t *testing.T) {
		c := buildWorld(t, world)
		if err := c.Unlock("B1", 0); !errors.Is(err, wave.ErrBadState) {
			t.Fatalf("unlock unlocked got %v", err)
		}
		_, err := c.Release([]string{"O1"})
		must(t, err)
		if _, err := c.Release([]string{"O1"}); !errors.Is(err, wave.ErrBadState) {
			t.Fatalf("re-release got %v", err)
		}
		must(t, c.AddOrder("N", 0, []wave.Line{{"x", 1}}))
		if _, err := c.Release([]string{"O1", "N"}); !errors.Is(err, wave.ErrBadState) {
			t.Fatalf("mixed allocated got %v", err)
		}
		must(t, c.ShortPick("O1", "K1", 0))
		if err := c.PutStock("K1", slot.Pick, "x", 1); !errors.Is(err, wave.ErrBadState) {
			t.Fatalf("put locked got %v", err)
		}
	})

	t.Run("qty", func(t *testing.T) {
		c := buildWorld(t, world)
		_, err := c.Release([]string{"O1"})
		must(t, err)
		if err := c.Pick("O1", "K1", 4); !errors.Is(err, wave.ErrQtyMismatch) {
			t.Fatalf("pick wrong qty got %v", err)
		}
		if err := c.ShortPick("O1", "K1", 5); !errors.Is(err, wave.ErrQtyMismatch) {
			t.Fatalf("found==qty got %v", err)
		}
		if err := c.ShortPick("O1", "K1", 9); !errors.Is(err, wave.ErrQtyMismatch) {
			t.Fatalf("found>qty got %v", err)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		c := buildWorld(t, world)
		if err := c.SetPallet("x", 20); !errors.Is(err, wave.ErrConflict) {
			t.Fatalf("reset pallet got %v", err)
		}
		if err := c.PutStock("B1", slot.Pick, "x", 1); !errors.Is(err, wave.ErrConflict) {
			t.Fatalf("kind change got %v", err)
		}
		if err := func() error {
			if e := c.SetPallet("y", 1); e != nil {
				return e
			}
			return c.PutStock("B1", slot.Bulk, "y", 1)
		}(); !errors.Is(err, wave.ErrConflict) {
			t.Fatalf("sku change got %v", err)
		}
		if err := c.AddOrder("O1", 0, []wave.Line{{"x", 1}}); !errors.Is(err, wave.ErrConflict) {
			t.Fatalf("dup order got %v", err)
		}
	})
}

func TestRejectChangesNothing(t *testing.T) {
	c := buildWorld(t, testWorld{
		pallets: map[string]int64{"x": 10},
		locs:    []locSpec{{"B1", slot.Bulk, "x", 10}},
	})
	must(t, c.AddOrder("O1", 0, []wave.Line{{"x", 6}}))
	must(t, c.AddOrder("O2", 0, []wave.Line{{"x", 6}}))
	// 整批只能满足一个：O1 成功、O2 Backorder；随后错误操作不得改状态
	st2x, _, _ := c.Status("O2")
	if st2x != wave.StatusNew {
		t.Fatalf("pre: O2=%v", st2x)
	}
	res, err := c.Release([]string{"O1", "O2"})
	must(t, err)
	if res[0].State != wave.StatusAllocated || res[1].State != wave.StatusBackorder {
		t.Fatalf("res=%+v %+v", res[0], res[1])
	}
	// 各种被拒操作
	_ = c.Pick("O2", "B1", 1)      // 记录不存在
	_ = c.Pick("O1", "B1", 2)      // 数量不符
	_ = c.ShortPick("O1", "B1", 6) // found==qty
	_ = c.Unlock("B1", 0)          // 未锁定
	_ = c.Cancel("O2")             // 非 Allocated
	before := allocsOf(c, "O1")
	st1, _, _ := c.Status("O1")
	st2, _, _ := c.Status("O2")
	l, _ := c.LocView("B1")
	if !eqMap(allocsOf(c, "O1"), before) ||
		st1 != wave.StatusAllocated || st2 != wave.StatusBackorder ||
		l.OnHand != 10 || l.Reserved != 6 || l.Locked {
		t.Fatalf("state changed by rejected ops: o1=%v o2=%v loc=%+v", allocsOf(c, "O1"), st2, l)
	}
}

func TestShortPickMultiOrderAndShortfall(t *testing.T) {
	// B1 整托 30（3 托），K1 拣选 2；O1(高) 20 → B1:20；O2(低) 10 → B1:10；
	// K1 上还有 O3 的预占 2。ShortPick(O1,B1,15)：B1 锁定，O1 缺 5，O2 删 10，O3 删 2。
	c := buildWorld(t, testWorld{
		pallets: map[string]int64{"x": 10},
		locs: []locSpec{
			{"B1", slot.Bulk, "x", 30},
			{"K1", slot.Pick, "x", 2},
			{"B2", slot.Bulk, "x", 8},
		},
	})
	must(t, c.AddOrder("O1", 9, []wave.Line{{"x", 20}}))
	must(t, c.AddOrder("O2", 5, []wave.Line{{"x", 10}}))
	must(t, c.AddOrder("O3", 5, []wave.Line{{"x", 2}}))
	res, err := c.Release([]string{"O1", "O2", "O3"})
	must(t, err)
	for _, r := range res {
		if r.State != wave.StatusAllocated {
			t.Fatalf("%s not allocated: %v", r.Order, r.Err)
		}
	}
	if got := allocsOf(c, "O1"); !eqMap(got, map[string]int64{"B1": 20}) {
		t.Fatalf("O1 %v", got)
	}
	if got := allocsOf(c, "O2"); !eqMap(got, map[string]int64{"B1": 10}) {
		t.Fatalf("O2 %v", got)
	}
	if got := allocsOf(c, "O3"); !eqMap(got, map[string]int64{"K1": 2}) {
		t.Fatalf("O3 %v", got)
	}
	// ShortPick O1 at B1, found=15；O1 缺 5，O2 缺 10，O3 缺 2；可用仅剩 B2=8、K1=2
	must(t, c.ShortPick("O1", "B1", 15))
	// O3 在 K1，不受 B1 短拣影响，K1 可用仍被 O3 占满。
	// 重分配队列仅 O1(5)、O2(10)：
	// O1: Pick K1 可用 0 → 回补 B2 取 5，满足；
	// O2: 回补 B2 余 3 全取 → 缺 7 记缺口；O3 不变。
	if got := allocsOf(c, "O1"); !eqMap(got, map[string]int64{"B2": 5}) {
		t.Fatalf("O1 after %v", got)
	}
	if got := allocsOf(c, "O2"); !eqMap(got, map[string]int64{"B2": 3}) {
		t.Fatalf("O2 after %v", got)
	}
	if got := allocsOf(c, "O3"); !eqMap(got, map[string]int64{"K1": 2}) {
		t.Fatalf("O3 after %v", got)
	}
	st, short, _ := c.Status("O1")
	if st != wave.StatusAllocated || short != 0 {
		t.Fatalf("O1 st=%v short=%d", st, short)
	}
	st, short, _ = c.Status("O2")
	if st != wave.StatusAllocated || short != 7 {
		t.Fatalf("O2 st=%v short=%d", st, short)
	}
	st, short, _ = c.Status("O3")
	if st != wave.StatusAllocated || short != 0 {
		t.Fatalf("O3 st=%v short=%d", st, short)
	}
	l, _ := c.LocView("B1")
	if !l.Locked || l.OnHand != 15 || l.Reserved != 0 {
		t.Fatalf("B1 locked=%v onHand=%d reserved=%d", l.Locked, l.OnHand, l.Reserved)
	}
	// O1 拣 B2:5 后 Done；O2 拣 B2:3 后 Short（缺口 7）；O3 仍在 K1。
	must(t, c.Pick("O1", "B2", 5))
	st, short, _ = c.Status("O1")
	if st != wave.StatusDone || short != 0 {
		t.Fatalf("O1 final st=%v short=%d", st, short)
	}
	must(t, c.Pick("O2", "B2", 3))
	st, short, _ = c.Status("O2")
	if st != wave.StatusShort || short != 7 {
		t.Fatalf("O2 final st=%v short=%d", st, short)
	}
	// Unlock B1 盘点 6 后可再分配：重新 Release 不行（Short 不可 Release）；
	// 用新订单验证库位已可用
	must(t, c.Unlock("B1", 6))
	l, _ = c.LocView("B1")
	if l.Locked || l.OnHand != 6 || l.Reserved != 0 {
		t.Fatalf("B1 after unlock: %+v", l)
	}
	must(t, c.AddOrder("N", 0, []wave.Line{{"x", 6}}))
	res, err = c.Release([]string{"N"})
	must(t, err)
	if res[0].State != wave.StatusAllocated {
		t.Fatalf("N=%v", res[0].State)
	}
}

func TestCancelReleasesReservations(t *testing.T) {
	c := buildWorld(t, testWorld{
		pallets: map[string]int64{"x": 10},
		locs:    []locSpec{{"K1", slot.Pick, "x", 5}},
	})
	must(t, c.AddOrder("O", 0, []wave.Line{{"x", 5}}))
	_, err := c.Release([]string{"O"})
	must(t, err)
	must(t, c.Cancel("O"))
	st, _, _ := c.Status("O")
	if st != wave.StatusCancelled {
		t.Fatalf("st=%v", st)
	}
	l, _ := c.LocView("K1")
	if l.Reserved != 0 || l.OnHand != 5 {
		t.Fatalf("K1 %+v", l)
	}
	must(t, c.AddOrder("O2", 0, []wave.Line{{"x", 5}}))
	res, err := c.Release([]string{"O2"})
	must(t, err)
	if res[0].State != wave.StatusAllocated {
		t.Fatalf("O2=%v", res[0].State)
	}
}

func TestProbedBoundIndependentOfOtherSKUs(t *testing.T) {
	for _, nOther := range []int{100, 10000} {
		c := wave.New()
		must(t, c.SetPallet("x", 10))
		must(t, c.SetPallet("z", 10))
		for i := 0; i < 7; i++ {
			loc := string(rune('A' + i))
			must(t, c.PutStock(loc, slot.Bulk, "x", 1))
		}
		for i := 0; i < 3; i++ {
			loc := "K" + string(rune('A'+i))
			must(t, c.PutStock(loc, slot.Pick, "x", 1))
		}
		for i := 0; i < nOther; i++ {
			must(t, c.PutStock(
				"Z"+string(rune('a'+i/26))+string(rune('a'+i%26)),
				slot.Bulk, "z", 1))
		}
		must(t, c.AddOrder("O", 0, []wave.Line{{"x", 1000}}))
		res, err := c.Release([]string{"O"})
		must(t, err)
		if res[0].State != wave.StatusBackorder {
			t.Fatalf("nOther=%d state=%v", nOther, res[0].State)
		}
		p := c.ProbedCount()
		// x 共 10 个库位；整托 7 + Pick 3 + 回补 7 = 17 ≤ 2*10=20
		total := p.BulkPallet + p.Pick + p.BulkTail
		if total > 20 {
			t.Fatalf("nOther=%d probed=%+v total=%d > 20", nOther, p, total)
		}
		if p.BulkPallet != 7 || p.Pick != 3 || p.BulkTail != 7 {
			t.Fatalf("nOther=%d probed=%+v", nOther, p)
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	c := wave.New()
	// 每个 SKU 独立 5 个库位 × 10，避免跨 goroutine 抢货导致 Backorder。
	const skuCount = 16 * 30
	for i := 0; i < skuCount; i++ {
		sku := "s" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		must(t, c.SetPallet(sku, 10))
		must(t, c.PutStock("L"+string(rune('A'+i/26))+string(rune('a'+i%26)),
			slot.Pick, sku, 10))
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				idx := g*30 + i
				sku := "s" + string(rune('A'+idx/26)) + string(rune('a'+idx%26))
				id := "O" + string(rune('A'+g)) + string(rune('a'+i))
				if err := c.AddOrder(id, g%10, []wave.Line{{sku, 3}}); err != nil {
					t.Errorf("add %s: %v", id, err)
					return
				}
				res, err := c.Release([]string{id})
				if err != nil {
					t.Errorf("release %s: %v", id, err)
					return
				}
				if res[0].State != wave.StatusAllocated {
					t.Errorf("order %s state=%v", id, res[0].State)
					return
				}
				rows, err := c.OrderAllocs(id)
				if err != nil {
					t.Errorf("allocs %s: %v", id, err)
					return
				}
				var sum int64
				for _, r := range rows {
					sum += r.Qty
				}
				if sum != 3 {
					t.Errorf("order %s sum=%d rows=%v", id, sum, rows)
				}
				for _, r := range rows {
					if err := c.Pick(id, r.Loc, r.Qty); err != nil {
						t.Errorf("pick %s %s: %v", id, r.Loc, err)
					}
				}
				st, _, err := c.Status(id)
				if err != nil || st != wave.StatusDone {
					t.Errorf("status %s=%v err=%v", id, st, err)
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestReplayDeterminism 同一操作序列在两个实例上重放，结果逐字节一致。

// TestReplayDeterminism 同一操作序列在两个实例上重放，结果字符串完全一致。
func TestReplayDeterminism(t *testing.T) {
	play := func() string {
		c := wave.New()
		var sb strings.Builder
		must(t, c.SetPallet("x", 10))
		locs := []struct {
			id string
			k  slot.Kind
			q  int64
		}{{"B1", slot.Bulk, 25}, {"B2", slot.Bulk, 10}, {"K1", slot.Pick, 4}, {"K2", slot.Pick, 3}}
		for _, l := range locs {
			must(t, c.PutStock(l.id, l.k, "x", l.q))
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			must(t, c.AddOrder(id, int(id[0]-'a'), []wave.Line{{"x", 7}}))
		}
		res, err := c.Release([]string{"d", "b", "a", "c"})
		must(t, err)
		for _, r := range res {
			fmt.Fprintf(&sb, "%s:%v;", r.Order, r.State)
		}
		// d 优先级最高，q=7 → 整托步 k=0 → Pick K1=4、K2=3；短拣 K1（found 1）
		must(t, c.ShortPick("d", "K1", 1))
		for _, id := range []string{"a", "b", "c", "d"} {
			rows, _ := c.OrderAllocs(id)
			for _, a := range rows {
				fmt.Fprintf(&sb, "%s@%s=%d;", id, a.Loc, a.Qty)
			}
			st, sh, _ := c.Status(id)
			fmt.Fprintf(&sb, "%s#%v/%d;", id, st, sh)
		}
		return sb.String()
	}
	if s1, s2 := play(), play(); s1 != s2 {
		t.Fatalf("replay differs:\n%s\nvs\n%s", s1, s2)
	}
}

// TestShortPickTouchesOnlyLocRecords L 上 n 条、O 上 m 条预占；短拣 L 后
// L 上 n 条全部处理（锁定且 reserved=0），O 上 m 条原样保留。
func TestShortPickTouchesOnlyLocRecords(t *testing.T) {
	for _, nm := range [][2]int{{1, 3}, {5, 7}, {20, 50}} {
		n, m := nm[0], nm[1]
		c := wave.New()
		must(t, c.SetPallet("x", 100))
		must(t, c.PutStock("L", slot.Pick, "x", int64(n)))
		must(t, c.PutStock("O", slot.Pick, "x", int64(m)))
		mkOrders := func(prefix string, count int) []string {
			var ids []string
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("%s%02d", prefix, i)
				ids = append(ids, id)
				must(t, c.AddOrder(id, i%10, []wave.Line{{"x", 1}}))
				_, err := c.Release([]string{id})
				must(t, err)
			}
			return ids
		}
		idsL := mkOrders("L", n) // 全部落在容量恰好 n 的 L 上
		idsO := mkOrders("O", m) // L 已满，全部落 O
		ov, _ := c.LocView("O")
		if ov.Reserved != int64(m) {
			t.Fatalf("O reserved=%d want %d", ov.Reserved, m)
		}
		must(t, c.ShortPick(idsL[0], "L", 0))
		lv, _ := c.LocView("L")
		if !lv.Locked || lv.Reserved != 0 {
			t.Fatalf("n=%d L=%+v", n, lv)
		}
		ov, _ = c.LocView("O")
		if ov.Locked || ov.Reserved != int64(m) {
			t.Fatalf("n=%d m=%d O=%+v want reserved=%d untouched", n, m, ov, m)
		}
		for _, id := range idsO {
			if got := allocsOf(c, id); got["O"] != 1 {
				t.Fatalf("order %s lost alloc: %v", id, got)
			}
		}
	}
}
