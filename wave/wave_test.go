package wave

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology"
	"ontology/slot"
)

func eqDetail(a, b []LocQty) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func newExample(t *testing.T) *Manager {
	t.Helper()
	m := NewManager()
	checkT(t, m.SetPallet("x", 10))
	checkT(t, m.PutStock("B1", slot.Bulk, "x", 25))
	checkT(t, m.PutStock("B2", slot.Bulk, "x", 10))
	checkT(t, m.PutStock("K1", slot.Pick, "x", 4))
	checkT(t, m.PutStock("K2", slot.Pick, "x", 3))
	return m
}

func checkT(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func addRelease(t *testing.T, m *Manager, id ontology.ID, pri int, lines []Line) []OrderResult {
	t.Helper()
	checkT(t, m.AddOrder(id, pri, lines))
	r, err := m.Release([]ontology.ID{id})
	checkT(t, err)
	return r
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// 题面三行分配例子。
func TestAllocationExamples(t *testing.T) {
	{
		m := newExample(t)
		r := addRelease(t, m, "O1", 5, []Line{{"x", 28}})
		if !r[0].OK || !eqDetail(r[0].Lines, []LocQty{{"B1", 21}, {"K1", 4}, {"K2", 3}}) {
			t.Fatalf("q28 got %+v", r[0])
		}
	}
	{
		m := newExample(t)
		r := addRelease(t, m, "O1", 5, []Line{{"x", 30}})
		if !r[0].OK || !eqDetail(r[0].Lines, []LocQty{{"B1", 20}, {"B2", 10}}) {
			t.Fatalf("q30 got %+v", r[0])
		}
	}
	{
		m := newExample(t)
		r := addRelease(t, m, "O1", 5, []Line{{"x", 9}})
		if !r[0].OK || !eqDetail(r[0].Lines, []LocQty{{"B2", 2}, {"K1", 4}, {"K2", 3}}) {
			t.Fatalf("q9 got %+v", r[0])
		}
	}
}

// 整托不足时余量转 Pick；回补并列按编号。
func TestPalletShortfallToPickAndTie(t *testing.T) {
	m := NewManager()
	checkT(t, m.SetPallet("x", 10))
	checkT(t, m.PutStock("B1", slot.Bulk, "x", 10))
	checkT(t, m.PutStock("B2", slot.Bulk, "x", 10))
	checkT(t, m.PutStock("K1", slot.Pick, "x", 8))
	r := addRelease(t, m, "O1", 5, []Line{{"x", 23}})
	want := []LocQty{{"B1", 10}, {"B2", 10}, {"K1", 3}}
	if !r[0].OK || !eqDetail(r[0].Lines, want) {
		t.Fatalf("got %+v want %v", r[0], want)
	}
	m2 := NewManager()
	checkT(t, m2.SetPallet("x", 10))
	checkT(t, m2.PutStock("B1", slot.Bulk, "x", 5))
	checkT(t, m2.PutStock("B2", slot.Bulk, "x", 5))
	r2 := addRelease(t, m2, "O1", 5, []Line{{"x", 8}})
	want2 := []LocQty{{"B1", 5}, {"B2", 3}}
	if !r2[0].OK || !eqDetail(r2[0].Lines, want2) {
		t.Fatalf("tie got %+v want %v", r2[0], want2)
	}
}

// 整单全有或全无：一行失败撤销整单，后续订单受益于释放出的库存。
func TestAllOrNothingAndRollbackBenefit(t *testing.T) {
	m := NewManager()
	checkT(t, m.SetPallet("x", 10))
	checkT(t, m.SetPallet("y", 10))
	checkT(t, m.PutStock("B1", slot.Bulk, "x", 10))
	checkT(t, m.PutStock("B2", slot.Bulk, "y", 5))
	checkT(t, m.AddOrder("O1", 9, []Line{{"x", 10}, {"y", 10}}))
	checkT(t, m.AddOrder("O2", 1, []Line{{"x", 10}}))
	res, err := m.Release([]ontology.ID{"O1", "O2"})
	checkT(t, err)
	if res[0].Order != "O1" || res[0].OK {
		t.Fatalf("O1 should fail: %+v", res[0])
	}
	if !res[1].OK || !eqDetail(res[1].Lines, []LocQty{{"B1", 10}}) {
		t.Fatalf("O2 should take released B1: %+v", res[1])
	}
	st, _ := m.Status("O1")
	if st != Backorder {
		t.Fatalf("O1 status=%v want Backorder", st)
	}
	l, _ := m.Store().Get("B1")
	if l.Reserved != 10 {
		t.Fatalf("B1 reserved=%d want 10", l.Reserved)
	}
	r1b, err := m.Release([]ontology.ID{"O1"})
	checkT(t, err)
	if r1b[0].OK {
		t.Fatal("O1 still cannot be fully allocated")
	}
}

// Release 批级校验：任一不过整批拒绝且零状态变更。
func TestReleaseBatchValidation(t *testing.T) {
	m := newExample(t)
	addRelease(t, m, "O1", 5, []Line{{"x", 1}})
	if _, err := m.Release([]ontology.ID{"O2", "O2"}); !errors.Is(err, ontology.ErrArgument) {
		t.Fatalf("dup ids got %v", err)
	}
	if _, err := m.Release([]ontology.ID{"ZZ"}); !errors.Is(err, ontology.ErrNotFound) {
		t.Fatalf("missing got %v", err)
	}
	if _, err := m.Release([]ontology.ID{"O1"}); !errors.Is(err, ontology.ErrState) {
		t.Fatalf("allocated got %v", err)
	}
	d, _ := m.Detail("O1")
	if !eqDetail(d, []LocQty{{"K1", 1}}) {
		t.Fatalf("state changed after rejected release: %v", d)
	}
}

// 短拣：锁定、重分配、Unlock 后再分配（题面第二例）。
func TestShortPickReallocationAndUnlock(t *testing.T) {
	m := newExample(t)
	checkT(t, m.AddOrder("O1", 9, []Line{{"x", 28}}))
	checkT(t, m.AddOrder("O2", 5, []Line{{"x", 5}}))
	_, err := m.Release([]ontology.ID{"O1", "O2"})
	checkT(t, err)
	checkT(t, m.ShortPick("O1", "K1", 1))
	d1, _ := m.Detail("O1")
	if !eqDetail(d1, []LocQty{{"B1", 21}, {"B2", 3}, {"K2", 3}}) {
		t.Fatalf("O1 after short %v", d1)
	}
	sf, _ := m.Shortfall("O1")
	if sf != 0 {
		t.Fatalf("O1 gap=%d want 0", sf)
	}
	l, _ := m.Store().Get("K1")
	if !l.Locked || l.OnHand != 3 {
		t.Fatalf("K1 = %+v", l)
	}
	checkT(t, m.Unlock("K1", 10))
	checkT(t, m.AddOrder("O3", 1, []Line{{"x", 2}}))
	r, err := m.Release([]ontology.ID{"O3"})
	checkT(t, err)
	if !r[0].OK || !eqDetail(r[0].Lines, []LocQty{{"K1", 2}}) {
		t.Fatalf("O3 after unlock: %+v", r[0])
	}
}

// 多订单同库位短拣：本单差额最先，其余按优先级/编号；失败记缺口。
func TestShortPickMultipleOrdersAndGap(t *testing.T) {
	m := NewManager()
	checkT(t, m.SetPallet("x", 10))
	checkT(t, m.PutStock("K1", slot.Pick, "x", 100))
	checkT(t, m.AddOrder("OA", 5, []Line{{"x", 30}}))
	checkT(t, m.AddOrder("OB", 9, []Line{{"x", 20}}))
	checkT(t, m.AddOrder("OC", 9, []Line{{"x", 10}}))
	_, err := m.Release([]ontology.ID{"OA", "OB", "OC"})
	checkT(t, err)
	checkT(t, m.ShortPick("OA", "K1", 0))
	for _, c := range []struct {
		id  ontology.ID
		gap int64
		st  Status
	}{
		{"OA", 30, Short}, {"OB", 20, Short}, {"OC", 10, Short},
	} {
		gap, _ := m.Shortfall(c.id)
		if gap != c.gap {
			t.Fatalf("%s gap=%d want %d", c.id, gap, c.gap)
		}
		st, _ := m.Status(c.id)
		if st != c.st {
			t.Fatalf("%s status=%v want %v", c.id, st, c.st)
		}
	}
	if m.lastTouched != 3 {
		t.Fatalf("touched=%d want 3", m.lastTouched)
	}
	if _, err := m.Release([]ontology.ID{"OA"}); !errors.Is(err, ontology.ErrState) {
		t.Fatalf("release Short got %v", err)
	}
}

// touched 只与该库位上的记录数有关，与其他库位记录数无关。
func TestTouchedDependsOnlyOnLocRecords(t *testing.T) {
	touchedByExtraLocs := map[int]int{}
	for _, extraLocs := range []int{0, 30} {
		m := NewManager()
		checkT(t, m.SetPallet("x", 10))
		checkT(t, m.PutStock("K1", slot.Pick, "x", 3))
		for i := 0; i < extraLocs; i++ {
			checkT(t, m.PutStock(ontology.ID("Z"+itoa(i)), slot.Pick, "x", 1))
		}
		for _, id := range []string{"A", "B", "C"} {
			checkT(t, m.AddOrder(ontology.ID("O"+id), 5, []Line{{"x", 1}}))
		}
		var extraRel []ontology.ID
		for i := 0; i < extraLocs; i++ {
			id := ontology.ID("E" + itoa(i))
			checkT(t, m.AddOrder(id, 5, []Line{{"x", 1}}))
			extraRel = append(extraRel, id)
		}
		_, err := m.Release([]ontology.ID{"OA", "OB", "OC"})
		checkT(t, err)
		if extraLocs > 0 {
			_, err = m.Release(extraRel)
			checkT(t, err)
		}
		checkT(t, m.ShortPick("OA", "K1", 0))
		touchedByExtraLocs[extraLocs] = m.lastTouched
	}
	if touchedByExtraLocs[0] != 3 || touchedByExtraLocs[0] != touchedByExtraLocs[30] {
		t.Fatalf("touched should be 3 regardless of other locs, got %+v", touchedByExtraLocs)
	}
}

// Pick / Cancel / Done 状态流。
func TestPickDoneAndCancel(t *testing.T) {
	m := newExample(t)
	r := addRelease(t, m, "O1", 5, []Line{{"x", 4}})
	if !eqDetail(r[0].Lines, []LocQty{{"K1", 4}}) {
		t.Fatalf("setup %v", r[0].Lines)
	}
	if err := m.Pick("O1", "K1", 3); !errors.Is(err, ontology.ErrQuantity) {
		t.Fatalf("pick mismatch got %v", err)
	}
	if err := m.Pick("O1", "K2", 4); !errors.Is(err, ontology.ErrNotFound) {
		t.Fatalf("pick missing record got %v", err)
	}
	checkT(t, m.Pick("O1", "K1", 4))
	if st, _ := m.Status("O1"); st != Done {
		t.Fatalf("status=%v want Done", st)
	}
	if err := m.Pick("O1", "K1", 4); !errors.Is(err, ontology.ErrNotFound) {
		t.Fatalf("double pick got %v", err)
	}
	addRelease(t, m, "O2", 5, []Line{{"x", 3}})
	checkT(t, m.Cancel("O2"))
	if st, _ := m.Status("O2"); st != Cancelled {
		t.Fatalf("O2=%v want Cancelled", st)
	}
	if err := m.Cancel("O2"); !errors.Is(err, ontology.ErrState) {
		t.Fatalf("cancel twice got %v", err)
	}
	if err := m.Cancel("NOPE"); !errors.Is(err, ontology.ErrNotFound) {
		t.Fatalf("cancel missing got %v", err)
	}
	r3 := addRelease(t, m, "O3", 5, []Line{{"x", 3}})
	if !r3[0].OK || !eqDetail(r3[0].Lines, []LocQty{{"K2", 3}}) {
		t.Fatalf("O3 after cancel: %+v", r3[0])
	}
}

// 五类错误次序：参数 > 不存在 > 状态 > 数量 > 冲突；被拒不改状态。
func TestErrorOrdering(t *testing.T) {
	m := newExample(t)
	addRelease(t, m, "O1", 5, []Line{{"x", 4}})
	if err := m.AddOrder("", 5, nil); !errors.Is(err, ontology.ErrArgument) {
		t.Fatalf("add bad got %v", err)
	}
	if err := m.AddOrder("dup", 5, []Line{{"x", 1}}); err != nil {
		t.Fatal(err)
	}
	if err := m.AddOrder("dup", 5, []Line{{"x", 1}}); !errors.Is(err, ontology.ErrConflict) {
		t.Fatalf("dup order got %v", err)
	}
	if err := m.AddOrder("bad", 5, []Line{{"x", 1}, {"x", 2}}); !errors.Is(err, ontology.ErrArgument) {
		t.Fatalf("dup line got %v", err)
	}
	if err := m.Pick("ZZ", "K1", 1_000_000_001); !errors.Is(err, ontology.ErrArgument) {
		t.Fatalf("bad qty first got %v", err)
	}
	if err := m.Pick("ZZ", "K1", 1); !errors.Is(err, ontology.ErrNotFound) {
		t.Fatalf("missing order got %v", err)
	}
	if err := m.Pick("O1", "NOPE", 1); !errors.Is(err, ontology.ErrNotFound) {
		t.Fatalf("missing loc got %v", err)
	}
	addRelease(t, m, "O2", 5, []Line{{"x", 3}})
	checkT(t, m.Cancel("O2"))
	if err := m.Pick("O2", "K2", 99); !errors.Is(err, ontology.ErrNotFound) {
		t.Fatalf("pick cancelled got %v want ErrNotFound", err)
	}
	if err := m.ShortPick("O1", "K1", 4); !errors.Is(err, ontology.ErrQuantity) {
		t.Fatalf("found==q got %v", err)
	}
	if err := m.ShortPick("O1", "K1", 5); !errors.Is(err, ontology.ErrQuantity) {
		t.Fatalf("found>q got %v", err)
	}
	if err := m.Unlock("K1", 0); !errors.Is(err, ontology.ErrState) {
		t.Fatalf("unlock unlocked got %v", err)
	}
	if err := m.Unlock("NOPE", 0); !errors.Is(err, ontology.ErrNotFound) {
		t.Fatalf("unlock missing got %v", err)
	}
	if st, _ := m.Status("O1"); st != Allocated {
		t.Fatalf("O1 status=%v", st)
	}
	d, _ := m.Detail("O1")
	if !eqDetail(d, []LocQty{{"K1", 4}}) {
		t.Fatalf("O1 detail changed: %v", d)
	}
}

// probed 界限：一行考察库位数 ≤ 2·本SKU库位数，与其他 SKU 无关（100/10000 两档）。
func TestProbedBound(t *testing.T) {
	probedByOthers := map[int]int{}
	for _, otherLocs := range []int{100, 10000} {
		m := NewManager()
		checkT(t, m.SetPallet("x", 10))
		checkT(t, m.SetPallet("z", 10))
		checkT(t, m.PutStock("B1", slot.Bulk, "x", 25))
		checkT(t, m.PutStock("B2", slot.Bulk, "x", 10))
		checkT(t, m.PutStock("B3", slot.Bulk, "x", 7))
		checkT(t, m.PutStock("B4", slot.Bulk, "x", 3))
		checkT(t, m.PutStock("K1", slot.Pick, "x", 4))
		checkT(t, m.PutStock("K2", slot.Pick, "x", 3))
		checkT(t, m.PutStock("K3", slot.Pick, "x", 2))
		for i := 0; i < otherLocs; i++ {
			checkT(t, m.PutStock(ontology.ID("L"+itoa(i)), slot.Bulk, "z", 10))
		}
		addRelease(t, m, "O1", 5, []Line{{"x", 200}})
		if m.lastProbed > 2*7 {
			t.Fatalf("otherLocs=%d probed=%d > %d", otherLocs, m.lastProbed, 2*7)
		}
		probedByOthers[otherLocs] = m.lastProbed
	}
	if probedByOthers[100] != probedByOthers[10000] {
		t.Fatalf("probed differs with other-SKU locs: %+v", probedByOthers)
	}
}

// ---- 独立朴素逐步模拟参考实现 ----

type naiveLoc struct {
	kind             slot.Kind
	sku              string
	onHand, reserved int64
	locked           bool
}

type naiveOrder struct {
	id        string
	priority  int
	lines     []Line
	status    Status
	shortfall int64
}

type naive struct {
	pallets map[string]int64
	locs    map[string]*naiveLoc
	orders  map[string]*naiveOrder
	recs    map[string]map[string]int64
}

func newNaive() *naive {
	return &naive{
		pallets: map[string]int64{},
		locs:    map[string]*naiveLoc{},
		orders:  map[string]*naiveOrder{},
		recs:    map[string]map[string]int64{},
	}
}

func nValidID(s string) bool { return len(s) >= 1 && len(s) <= 32 }

func (n *naive) avail(loc string) int64 {
	l := n.locs[loc]
	if l.locked {
		return 0
	}
	return l.onHand - l.reserved
}

func (n *naive) locIDs(sku string, kind slot.Kind) []string {
	var out []string
	for id, l := range n.locs {
		if l.sku == sku && l.kind == kind {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// alloc 三步分配并即时落账；返回仍缺量。
func (n *naive) alloc(ord, sku string, q int64, tentative map[string]int64) int64 {
	p, ok := n.pallets[sku]
	if !ok {
		return q
	}
	take := func(loc string, amount int64) {
		n.locs[loc].reserved += amount
		if n.recs[ord] == nil {
			n.recs[ord] = map[string]int64{}
		}
		n.recs[ord][loc] += amount
		tentative[loc] += amount
		q -= amount
	}
	want := q / p
	gotP := int64(0)
	for _, loc := range n.locIDs(sku, slot.Bulk) {
		if gotP == want {
			break
		}
		pallets := n.avail(loc) / p
		if pallets > want-gotP {
			pallets = want - gotP
		}
		if pallets > 0 {
			take(loc, pallets*p)
			gotP += pallets
		}
	}
	if q > 0 {
		for _, loc := range n.locIDs(sku, slot.Pick) {
			if q == 0 {
				break
			}
			a := n.avail(loc)
			if a <= 0 {
				continue
			}
			if a > q {
				a = q
			}
			take(loc, a)
		}
	}
	if q > 0 {
		type c struct {
			id string
			a  int64
		}
		var cs []c
		for _, loc := range n.locIDs(sku, slot.Bulk) {
			if a := n.avail(loc); a > 0 {
				cs = append(cs, c{loc, a})
			}
		}
		sort.Slice(cs, func(i, j int) bool {
			if cs[i].a != cs[j].a {
				return cs[i].a < cs[j].a
			}
			return cs[i].id < cs[j].id
		})
		for _, x := range cs {
			if q == 0 {
				break
			}
			a := x.a
			if a > q {
				a = q
			}
			take(x.id, a)
		}
	}
	return q
}

func (n *naive) rollback(ord string, tentative map[string]int64) {
	for loc, q := range tentative {
		n.locs[loc].reserved -= q
		n.recs[ord][loc] -= q
		if n.recs[ord][loc] == 0 {
			delete(n.recs[ord], loc)
		}
		if len(n.recs[ord]) == 0 {
			delete(n.recs, ord)
		}
	}
}

func (n *naive) allocateOrder(o *naiveOrder) bool {
	tent := map[string]int64{}
	lines := append([]Line(nil), o.lines...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].SKU < lines[j].SKU })
	for _, ln := range lines {
		if n.alloc(o.id, string(ln.SKU), ln.Qty, tent) != 0 {
			n.rollback(o.id, tent)
			return false
		}
	}
	return true
}

func (n *naive) recompute(o *naiveOrder) {
	has := len(n.recs[o.id]) > 0
	switch {
	case !has && o.shortfall == 0:
		o.status = Done
	case o.shortfall > 0:
		o.status = Short
	default:
		o.status = Allocated
	}
}

func (n *naive) detail(id string) []LocQty {
	out := []LocQty{}
	for loc, q := range n.recs[id] {
		out = append(out, LocQty{ontology.ID(loc), q})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Loc < out[j].Loc })
	return out
}

func (n *naive) setPallet(sku string, p int64) error {
	if !nValidID(sku) || p < 1 || p > 1_000_000 {
		return ontology.ErrArgument
	}
	if _, ok := n.pallets[sku]; ok {
		return ontology.ErrConflict
	}
	n.pallets[sku] = p
	return nil
}

func (n *naive) putStock(loc string, kind slot.Kind, sku string, qty int64) error {
	if !nValidID(loc) || !nValidID(sku) || (kind != slot.Bulk && kind != slot.Pick) ||
		qty < 1 || qty > 1_000_000_000 {
		return ontology.ErrArgument
	}
	if l, ok := n.locs[loc]; ok {
		if l.locked {
			return ontology.ErrState
		}
		if l.kind != kind || l.sku != sku {
			return ontology.ErrConflict
		}
		l.onHand += qty
		return nil
	}
	if _, ok := n.pallets[sku]; !ok {
		return ontology.ErrNotFound
	}
	n.locs[loc] = &naiveLoc{kind: kind, sku: sku, onHand: qty}
	return nil
}

func (n *naive) addOrder(id string, pri int, lines []Line) error {
	if !nValidID(id) || pri < 0 || pri > 9 || len(lines) < 1 || len(lines) > 50 {
		return ontology.ErrArgument
	}
	seen := map[string]bool{}
	for _, ln := range lines {
		if !nValidID(string(ln.SKU)) || ln.Qty < 1 || ln.Qty > 1_000_000_000 || seen[string(ln.SKU)] {
			return ontology.ErrArgument
		}
		seen[string(ln.SKU)] = true
	}
	if _, ok := n.orders[id]; ok {
		return ontology.ErrConflict
	}
	cp := make([]Line, len(lines))
	copy(cp, lines)
	n.orders[id] = &naiveOrder{id: id, priority: pri, lines: cp, status: New}
	return nil
}

func (n *naive) release(ids []string) ([]OrderResult, error) {
	if len(ids) < 1 || len(ids) > 1000 {
		return nil, ontology.ErrArgument
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !nValidID(id) || seen[id] {
			return nil, ontology.ErrArgument
		}
		seen[id] = true
	}
	for _, id := range ids {
		o, ok := n.orders[id]
		if !ok {
			return nil, ontology.ErrNotFound
		}
		if o.status != New && o.status != Backorder {
			return nil, ontology.ErrState
		}
	}
	batch := make([]*naiveOrder, 0, len(ids))
	for _, id := range ids {
		batch = append(batch, n.orders[id])
	}
	sort.SliceStable(batch, func(i, j int) bool {
		if batch[i].priority != batch[j].priority {
			return batch[i].priority > batch[j].priority
		}
		return batch[i].id < batch[j].id
	})
	res := make([]OrderResult, 0, len(batch))
	for _, o := range batch {
		ok := n.allocateOrder(o)
		if ok {
			o.status = Allocated
		} else {
			o.status = Backorder
		}
		res = append(res, OrderResult{ontology.ID(o.id), ok, n.detail(o.id)})
	}
	return res, nil
}

func (n *naive) pick(oid, loc string, qty int64) error {
	if !nValidID(oid) || !nValidID(loc) || qty < 1 || qty > 1_000_000_000 {
		return ontology.ErrArgument
	}
	o, ok := n.orders[oid]
	if !ok {
		return ontology.ErrNotFound
	}
	l, lok := n.locs[loc]
	if !lok {
		return ontology.ErrNotFound
	}
	q, rok := n.recs[oid][loc]
	if !rok {
		return ontology.ErrNotFound
	}
	if o.status != Allocated && o.status != Short {
		return ontology.ErrState
	}
	if l.locked {
		return ontology.ErrState
	}
	if q != qty {
		return ontology.ErrQuantity
	}
	delete(n.recs[oid], loc)
	if len(n.recs[oid]) == 0 {
		delete(n.recs, oid)
	}
	l.onHand -= qty
	l.reserved -= qty
	n.recompute(o)
	return nil
}

func (n *naive) shortPick(oid, loc string, found int64) error {
	if !nValidID(oid) || !nValidID(loc) || found < 0 || found > 1_000_000_000 {
		return ontology.ErrArgument
	}
	o, ok := n.orders[oid]
	if !ok {
		return ontology.ErrNotFound
	}
	l, lok := n.locs[loc]
	if !lok {
		return ontology.ErrNotFound
	}
	q, rok := n.recs[oid][loc]
	if !rok {
		return ontology.ErrNotFound
	}
	if o.status != Allocated && o.status != Short {
		return ontology.ErrState
	}
	if l.locked {
		return ontology.ErrState
	}
	if found >= q {
		return ontology.ErrQuantity
	}
	delete(n.recs[oid], loc)
	if len(n.recs[oid]) == 0 {
		delete(n.recs, oid)
	}
	type def struct {
		o   *naiveOrder
		qty int64
	}
	affected := map[string]int64{}
	var reservedSum int64
	for other, rq := range n.recs {
		if r, has := rq[loc]; has {
			affected[other] = r
			reservedSum += r
			delete(rq, loc)
			if len(rq) == 0 {
				delete(n.recs, other)
			}
		}
	}
	l.onHand -= found
	l.reserved -= q + reservedSum
	l.locked = true
	defs := []def{{o, q - found}}
	for other, dq := range affected {
		defs = append(defs, def{n.orders[other], dq})
	}
	rest := defs[1:]
	sort.SliceStable(rest, func(i, j int) bool {
		if rest[i].o.priority != rest[j].o.priority {
			return rest[i].o.priority > rest[j].o.priority
		}
		return rest[i].o.id < rest[j].o.id
	})
	defs = append(defs[:1], rest...)
	touched := map[string]*naiveOrder{o.id: o}
	for _, d := range defs {
		tent := map[string]int64{}
		if n.alloc(d.o.id, l.sku, d.qty, tent) != 0 {
			n.rollback(d.o.id, tent)
			d.o.shortfall += d.qty
		}
		touched[d.o.id] = d.o
	}
	for _, ro := range touched {
		n.recompute(ro)
	}
	return nil
}

func (n *naive) unlock(loc string, counted int64) error {
	if !nValidID(loc) || counted < 0 || counted > 1_000_000_000 {
		return ontology.ErrArgument
	}
	l, ok := n.locs[loc]
	if !ok {
		return ontology.ErrNotFound
	}
	if !l.locked {
		return ontology.ErrState
	}
	l.locked = false
	l.onHand = counted
	l.reserved = 0
	return nil
}

func (n *naive) cancel(oid string) error {
	if !nValidID(oid) {
		return ontology.ErrArgument
	}
	o, ok := n.orders[oid]
	if !ok {
		return ontology.ErrNotFound
	}
	if o.status != Allocated {
		return ontology.ErrState
	}
	for loc, q := range n.recs[oid] {
		delete(n.recs[oid], loc)
		n.locs[loc].reserved -= q
	}
	delete(n.recs, oid)
	o.status = Cancelled
	return nil
}

// ---- 随机操作序列驱动生产代码与朴素模型 ----

const (
	opSetPallet = iota
	opPutStock
	opAddOrder
	opRelease
	opPick
	opShort
	opUnlock
	opCancel
)

type op struct {
	kind  int
	str1  string
	str2  string
	num   int64
	kind2 slot.Kind
	pri   int
	lines []Line
	ids   []string
}

func runOn(m *Manager, o op) ([]OrderResult, error) {
	switch o.kind {
	case opSetPallet:
		return nil, m.SetPallet(ontology.ID(o.str1), o.num)
	case opPutStock:
		return nil, m.PutStock(ontology.ID(o.str1), o.kind2, ontology.ID(o.str2), o.num)
	case opAddOrder:
		return nil, m.AddOrder(ontology.ID(o.str1), o.pri, o.lines)
	case opRelease:
		ids := make([]ontology.ID, len(o.ids))
		for i, s := range o.ids {
			ids[i] = ontology.ID(s)
		}
		return m.Release(ids)
	case opPick:
		return nil, m.Pick(ontology.ID(o.str1), ontology.ID(o.str2), o.num)
	case opShort:
		return nil, m.ShortPick(ontology.ID(o.str1), ontology.ID(o.str2), o.num)
	case opUnlock:
		return nil, m.Unlock(ontology.ID(o.str1), o.num)
	case opCancel:
		return nil, m.Cancel(ontology.ID(o.str1))
	}
	return nil, nil
}

func runOnNaive(n *naive, o op) ([]OrderResult, error) {
	switch o.kind {
	case opSetPallet:
		return nil, n.setPallet(o.str1, o.num)
	case opPutStock:
		return nil, n.putStock(o.str1, o.kind2, o.str2, o.num)
	case opAddOrder:
		return nil, n.addOrder(o.str1, o.pri, o.lines)
	case opRelease:
		return n.release(o.ids)
	case opPick:
		return nil, n.pick(o.str1, o.str2, o.num)
	case opShort:
		return nil, n.shortPick(o.str1, o.str2, o.num)
	case opUnlock:
		return nil, n.unlock(o.str1, o.num)
	case opCancel:
		return nil, n.cancel(o.str1)
	}
	return nil, nil
}

func genSeq(r *rand.Rand, nOps int) []op {
	const skus, locs, orders = 3, 8, 6
	skuName := func(i int) string { return fmt.Sprintf("S%d", i) }
	locName := func(i int) string { return fmt.Sprintf("L%02d", i) }
	ordName := func(i int) string { return fmt.Sprintf("O%d", i) }
	var ops []op
	for i := 0; i < skus; i++ {
		ops = append(ops, op{kind: opSetPallet, str1: skuName(i), num: int64(1 + r.Intn(12))})
	}
	for i := 0; i < locs; i++ {
		k := slot.Bulk
		if i%3 == 0 {
			k = slot.Pick
		}
		ops = append(ops, op{kind: opPutStock, str1: locName(i), kind2: k,
			str2: skuName(r.Intn(skus)), num: int64(1 + r.Intn(40))})
	}
	for len(ops) < nOps {
		switch r.Intn(8) {
		case opSetPallet:
			p := int64(1 + r.Intn(20))
			s := skuName(r.Intn(skus + 1))
			if r.Intn(10) == 0 {
				p = 0
			}
			ops = append(ops, op{kind: opSetPallet, str1: s, num: p})
		case opPutStock:
			k := slot.Bulk
			if r.Intn(2) == 0 {
				k = slot.Pick
			}
			ops = append(ops, op{kind: opPutStock, str1: locName(r.Intn(locs)),
				kind2: k, str2: skuName(r.Intn(skus)), num: int64(1 + r.Intn(40))})
		case opAddOrder:
			oi := r.Intn(orders)
			used := map[int]bool{}
			var ls []Line
			for j := 0; j < 1+r.Intn(3); j++ {
				si := r.Intn(skus)
				if used[si] {
					continue
				}
				used[si] = true
				ls = append(ls, Line{SKU: ontology.ID(skuName(si)), Qty: int64(1 + r.Intn(50))})
			}
			ops = append(ops, op{kind: opAddOrder, str1: ordName(oi), pri: r.Intn(10), lines: ls})
		case opRelease:
			used := map[string]bool{}
			var ids []string
			for j := 0; j < 1+r.Intn(4); j++ {
				id := ordName(r.Intn(orders))
				if !used[id] {
					used[id] = true
					ids = append(ids, id)
				}
			}
			ops = append(ops, op{kind: opRelease, ids: ids})
		case opPick:
			ops = append(ops, op{kind: opPick, str1: ordName(r.Intn(orders)),
				str2: locName(r.Intn(locs)), num: int64(1 + r.Intn(30))})
		case opShort:
			ops = append(ops, op{kind: opShort, str1: ordName(r.Intn(orders)),
				str2: locName(r.Intn(locs)), num: int64(r.Intn(30))})
		case opUnlock:
			ops = append(ops, op{kind: opUnlock, str1: locName(r.Intn(locs)),
				num: int64(r.Intn(40))})
		case opCancel:
			ops = append(ops, op{kind: opCancel, str1: ordName(r.Intn(orders))})
		}
	}
	return ops
}

func describe(o op) string {
	switch o.kind {
	case opSetPallet:
		return fmt.Sprintf("SetPallet(%q,%d)", o.str1, o.num)
	case opPutStock:
		return fmt.Sprintf("PutStock(%q,%v,%q,%d)", o.str1, o.kind2, o.str2, o.num)
	case opAddOrder:
		return fmt.Sprintf("AddOrder(%q,pri=%d,lines=%v)", o.str1, o.pri, o.lines)
	case opRelease:
		return fmt.Sprintf("Release(%v)", o.ids)
	case opPick:
		return fmt.Sprintf("Pick(%q,%q,%d)", o.str1, o.str2, o.num)
	case opShort:
		return fmt.Sprintf("ShortPick(%q,%q,found=%d)", o.str1, o.str2, o.num)
	case opUnlock:
		return fmt.Sprintf("Unlock(%q,%d)", o.str1, o.num)
	case opCancel:
		return fmt.Sprintf("Cancel(%q)", o.str1)
	}
	return "?"
}

func snapManager(b *bytes.Buffer, m *Manager) {
	b.Reset()
	fmt.Fprintln(b, "LOCATIONS:")
	var lids []string
	m.Store().RangeLocs(func(id string, _, _ int64, _ bool) { lids = append(lids, id) })
	sort.Strings(lids)
	for _, id := range lids {
		_, oh, rv, lk := m.Store().GetRaw(id)
		fmt.Fprintf(b, "  %s onHand=%d reserved=%d locked=%v\n", id, oh, rv, lk)
	}
	fmt.Fprintln(b, "ORDERS:")
	var ids []string
	for id := range m.orders {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		o := m.orders[ontology.ID(id)]
		fmt.Fprintf(b, "  %s status=%v gap=%d detail=%v\n", id, o.status, o.shortfall,
			m.sortedDetail(ontology.ID(id)))
	}
}

func snapNaive(b *bytes.Buffer, n *naive) {
	b.Reset()
	fmt.Fprintln(b, "LOCATIONS:")
	var lids []string
	for id := range n.locs {
		lids = append(lids, id)
	}
	sort.Strings(lids)
	for _, id := range lids {
		l := n.locs[id]
		fmt.Fprintf(b, "  %s onHand=%d reserved=%d locked=%v\n", id, l.onHand, l.reserved, l.locked)
	}
	fmt.Fprintln(b, "ORDERS:")
	var ids []string
	for id := range n.orders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o := n.orders[id]
		fmt.Fprintf(b, "  %s status=%v gap=%d detail=%v\n", id, o.status, o.shortfall, n.detail(id))
	}
}

func sameErr(a, b error) bool {
	return errors.Is(a, ontology.ErrArgument) == errors.Is(b, ontology.ErrArgument) &&
		errors.Is(a, ontology.ErrNotFound) == errors.Is(b, ontology.ErrNotFound) &&
		errors.Is(a, ontology.ErrState) == errors.Is(b, ontology.ErrState) &&
		errors.Is(a, ontology.ErrQuantity) == errors.Is(b, ontology.ErrQuantity) &&
		errors.Is(a, ontology.ErrConflict) == errors.Is(b, ontology.ErrConflict)
}

func sameResults(a, b []OrderResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Order != b[i].Order || a[i].OK != b[i].OK || !eqDetail(a[i].Lines, b[i].Lines) {
			return false
		}
	}
	return true
}

// TestRandomAgainstNaive 与逐步朴素模拟对照 1500 组随机操作序列。
func TestRandomAgainstNaive(t *testing.T) {
	if testing.Verbose() {
		t.Logf("对照 1500 组随机操作序列；每步比较错误类别、Release 明细、全量库存/预占/状态/缺口")
	}
	var bm, bn bytes.Buffer
	for seed := int64(1); seed <= 1500; seed++ {
		r := rand.New(rand.NewSource(seed))
		ops := genSeq(r, 60+r.Intn(60))
		m := NewManager()
		n := newNaive()
		var log bytes.Buffer
		fmt.Fprintf(&log, "=== seed=%d ops=%d ===\n", seed, len(ops))
		failed := false
		for step, o := range ops {
			rm, em := runOn(m, o)
			rn, en := runOnNaive(n, o)
			fmt.Fprintf(&log, "[%d] IN  %s\n", step, describe(o))
			if !sameErr(em, en) {
				failed = true
				fmt.Fprintf(&log, "    判定依据: 错误类别不一致 prod=%v naive=%v\n", em, en)
			}
			if o.kind == opRelease && !sameResults(rm, rn) {
				failed = true
				fmt.Fprintf(&log, "    判定依据: Release 结果不一致\n     prod=%+v\n     naive=%+v\n", rm, rn)
			}
			snapManager(&bm, m)
			snapNaive(&bn, n)
			if bm.String() != bn.String() {
				failed = true
				fmt.Fprintf(&log, "    判定依据: 全量快照不一致\n--- prod ---\n%s--- naive ---\n%s", bm.String(), bn.String())
			}
			if failed {
				break
			}
			fmt.Fprintf(&log, "    OUT ok (err=%v results=%d)\n", em, len(rm))
		}
		if failed {
			t.Fatalf("seed=%d 与朴素模拟不一致，日志如下:\n%s", seed, strings.TrimRight(log.String(), "\n"))
		}
		if testing.Verbose() && seed <= 3 {
			t.Logf("\n%s", strings.TrimRight(log.String(), "\n"))
		}
	}
}

// TestConcurrentSafe 并发混合调用后账实必须自洽：reserved == 记录之和且未锁时 ≤ onHand。
func TestConcurrentSafe(t *testing.T) {
	m := NewManager()
	checkT(t, m.SetPallet("x", 10))
	for _, loc := range []string{"B1", "B2", "K1", "K2"} {
		k := slot.Bulk
		if loc[0] == 'K' {
			k = slot.Pick
		}
		checkT(t, m.PutStock(ontology.ID(loc), k, "x", 1000))
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			const digits = "0123456789abcdef"
			id := ontology.ID("G" + string(digits[g]))
			_ = m.AddOrder(id, g%10, []Line{{SKU: "x", Qty: 5}})
			_, _ = m.Release([]ontology.ID{id})
			_ = m.Pick(id, "K1", 5)
			_ = m.Cancel(id)
		}(g)
	}
	wg.Wait()
	for _, loc := range []string{"B1", "B2", "K1", "K2"} {
		l, ok := m.Store().Get(ontology.ID(loc))
		if !ok {
			t.Fatal("loc missing")
		}
		var sum int64
		for _, q := range m.Ledger().OrdersAt(ontology.ID(loc)) {
			sum += q
		}
		if l.Reserved != sum {
			t.Fatalf("%s reserved=%d but ledger sum=%d", loc, l.Reserved, sum)
		}
		if !l.Locked && l.Reserved > l.OnHand {
			t.Fatalf("%s reserved %d > onHand %d", loc, l.Reserved, l.OnHand)
		}
	}
}
