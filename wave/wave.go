package wave

import (
	"sort"
	"sync"

	"ontology"
	"ontology/reserve"
	"ontology/slot"
)

// Status 是订单生命周期状态。
type Status int

const (
	New Status = iota + 1
	Allocated
	Backorder
	Short
	Done
	Cancelled
)

// Line 是一行需求。
type Line struct {
	SKU ontology.ID
	Qty int64
}

// LocQty 是一条按库位汇总的分配/预占明细。
type LocQty struct {
	Loc ontology.ID
	Qty int64
}

// OrderResult 是 Release 中单个订单的结果。
type OrderResult struct {
	Order ontology.ID
	OK    bool
	Lines []LocQty
}

type order struct {
	id        ontology.ID
	priority  int
	lines     []Line
	status    Status
	shortfall int64
}

// Manager 是波次预占器。一把互斥锁保护全部状态：
// 每个对外方法在锁内完成读改写，故并发调用等价于某种串行顺序。
type Manager struct {
	mu     sync.Mutex
	store  *slot.Store
	ledger *reserve.Ledger
	orders map[ontology.ID]*order

	// 非导出计数器，供同包测试证明扫描与触碰界限。
	lastProbed  int
	maxProbed   int
	lastTouched int
	maxTouched  int
}

func NewManager() *Manager {
	return &Manager{
		store:  slot.NewStore(),
		ledger: reserve.NewLedger(),
		orders: map[ontology.ID]*order{},
	}
}

// Store 与 Ledger 暴露底层账，供分层测试使用。
func (m *Manager) Store() *slot.Store      { return m.store }
func (m *Manager) Ledger() *reserve.Ledger { return m.ledger }

// Status 返回订单状态。
func (m *Manager) Status(id ontology.ID) (Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[id]
	if !ok {
		return 0, false
	}
	return o.status, true
}

// Shortfall 返回订单当前缺口。
func (m *Manager) Shortfall(id ontology.ID) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[id]
	if !ok {
		return 0, false
	}
	return o.shortfall, true
}

// Detail 返回订单当前全部未拣预占明细，按库位编号字节序。
func (m *Manager) Detail(id ontology.ID) ([]LocQty, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.orders[id]; !ok {
		return nil, false
	}
	return m.sortedDetail(id), true
}

func (m *Manager) sortedDetail(id ontology.ID) []LocQty {
	locs := m.ledger.LocsOf(id)
	out := make([]LocQty, 0, len(locs))
	for loc, q := range locs {
		out = append(out, LocQty{Loc: loc, Qty: q})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Loc < out[j].Loc })
	return out
}

// SetPallet / PutStock 配置库存，受同一把锁保护。
func (m *Manager) SetPallet(sku ontology.ID, p int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.SetPallet(sku, p)
}

func (m *Manager) PutStock(loc ontology.ID, kind slot.Kind, sku ontology.ID, qty int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.PutStock(loc, kind, sku, qty)
}

// AddOrder 录入订单；订单号重复为冲突。
func (m *Manager) AddOrder(id ontology.ID, priority int, lines []Line) error {
	if !ontology.ValidID(id) || priority < 0 || priority > 9 || len(lines) < 1 || len(lines) > 50 {
		return ontology.ErrArgument
	}
	seen := map[ontology.ID]struct{}{}
	for _, ln := range lines {
		if !ontology.ValidID(ln.SKU) || ln.Qty < 1 || ln.Qty > 1_000_000_000 {
			return ontology.ErrArgument
		}
		if _, dup := seen[ln.SKU]; dup {
			return ontology.ErrArgument
		}
		seen[ln.SKU] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.orders[id]; ok {
		return ontology.ErrConflict
	}
	cp := make([]Line, len(lines))
	copy(cp, lines)
	m.orders[id] = &order{id: id, priority: priority, lines: cp, status: New}
	return nil
}

// Release 整批校验，通过后按（priority 降序，订单号字节序）逐单分配。
func (m *Manager) Release(orderIDs []ontology.ID) ([]OrderResult, error) {
	if len(orderIDs) < 1 || len(orderIDs) > 1000 {
		return nil, ontology.ErrArgument
	}
	seen := map[ontology.ID]struct{}{}
	for _, id := range orderIDs {
		if !ontology.ValidID(id) {
			return nil, ontology.ErrArgument
		}
		if _, dup := seen[id]; dup {
			return nil, ontology.ErrArgument
		}
		seen[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// 批级检查：不存在先于状态，任一不过整批拒绝（零状态变更）。
	for _, id := range orderIDs {
		o, ok := m.orders[id]
		if !ok {
			return nil, ontology.ErrNotFound
		}
		if o.status != New && o.status != Backorder {
			return nil, ontology.ErrState
		}
	}
	batch := make([]*order, 0, len(orderIDs))
	for _, id := range orderIDs {
		batch = append(batch, m.orders[id])
	}
	sort.SliceStable(batch, func(i, j int) bool {
		if batch[i].priority != batch[j].priority {
			return batch[i].priority > batch[j].priority
		}
		return batch[i].id < batch[j].id
	})
	results := make([]OrderResult, 0, len(batch))
	for _, o := range batch {
		ok := m.allocateOrder(o)
		if ok {
			o.status = Allocated
		} else {
			o.status = Backorder
		}
		results = append(results, OrderResult{Order: o.id, OK: ok, Lines: m.sortedDetail(o.id)})
	}
	return results, nil
}

// allocateOrder 尝试整单分配；任一行失败则撤销本单本轮全部预占。
func (m *Manager) allocateOrder(o *order) bool {
	taken := map[ontology.ID]int64{}
	lines := append([]Line(nil), o.lines...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].SKU < lines[j].SKU })
	for _, ln := range lines {
		if _, need := m.allocFor(o.id, ln.SKU, ln.Qty, taken); need != 0 {
			for loc, q := range taken {
				m.ledger.Add(o.id, loc, -q)
				m.store.Release(loc, q)
			}
			return false
		}
	}
	return true
}

// allocFor 按三步规则为某订单的一行分配数量 q。
// tentative 记录本单本轮各库位的新增预占，供整单失败时撤销。
// 返回实际取得量与仍缺量（0 表示成功）。每步只看此刻可用量。
func (m *Manager) allocFor(ord ontology.ID, sku ontology.ID, q int64, tentative map[ontology.ID]int64) (int64, int64) {
	probed := 0
	defer func() {
		m.lastProbed = probed
		if probed > m.maxProbed {
			m.maxProbed = probed
		}
	}()
	p, ok := m.store.Pallet(sku)
	if !ok {
		return 0, q
	}
	need := q
	got := int64(0)
	take := func(loc ontology.ID, amount int64) {
		m.store.Reserve(loc, amount)
		m.ledger.Add(ord, loc, amount)
		tentative[loc] += amount
		need -= amount
		got += amount
	}
	// 步骤 1：整托，按编号字节序凑满 floor(q/P) 托即停。
	wantPallets := q / p
	gotPallets := int64(0)
	for _, loc := range m.store.BulkIDs(sku) {
		if wantPallets == gotPallets {
			break
		}
		probed++
		l, _ := m.store.Get(loc)
		pallets := l.Available() / p
		if pallets > wantPallets-gotPallets {
			pallets = wantPallets - gotPallets
		}
		if pallets > 0 {
			take(loc, pallets*p)
			gotPallets += pallets
		}
	}
	// 步骤 2：余量拆零，按编号字节序扫 Pick 位。
	if need > 0 {
		for _, loc := range m.store.PickIDs(sku) {
			if need == 0 {
				break
			}
			probed++
			l, _ := m.store.Get(loc)
			avail := l.Available()
			if avail <= 0 {
				continue
			}
			amount := avail
			if amount > need {
				amount = need
			}
			take(loc, amount)
		}
	}
	// 步骤 3：回补 Bulk 拆零，按此刻可用量升序、并列按编号，跳过 0。
	if need > 0 {
		type cand struct {
			loc   ontology.ID
			avail int64
		}
		var cands []cand
		for _, loc := range m.store.BulkIDs(sku) {
			probed++
			l, _ := m.store.Get(loc)
			if avail := l.Available(); avail > 0 {
				cands = append(cands, cand{loc, avail})
			}
		}
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].avail != cands[j].avail {
				return cands[i].avail < cands[j].avail
			}
			return cands[i].loc < cands[j].loc
		})
		for _, c := range cands {
			if need == 0 {
				break
			}
			amount := c.avail
			if amount > need {
				amount = need
			}
			take(c.loc, amount)
		}
	}
	return got, need
}

// Pick 按整条预占记录拣货：qty 须恰等于该记录未拣量。
func (m *Manager) Pick(oid, loc ontology.ID, qty int64) error {
	if !ontology.ValidID(oid) || !ontology.ValidID(loc) || qty < 1 || qty > 1_000_000_000 {
		return ontology.ErrArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[oid]
	if !ok {
		return ontology.ErrNotFound
	}
	l, lok := m.store.Get(loc)
	if !lok {
		return ontology.ErrNotFound
	}
	q, rok := m.ledger.Get(oid, loc)
	if !rok {
		return ontology.ErrNotFound
	}
	if o.status != Allocated && o.status != Short {
		return ontology.ErrState
	}
	if l.Locked {
		return ontology.ErrState
	}
	if q != qty {
		return ontology.ErrQuantity
	}
	m.ledger.Remove(oid, loc)
	m.store.Pick(loc, qty)
	m.recompute(o)
	return nil
}

// ShortPick 短拣：found 实拣，锁定库位、删除其上全部预占并按序重分配。
func (m *Manager) ShortPick(oid, loc ontology.ID, found int64) error {
	if !ontology.ValidID(oid) || !ontology.ValidID(loc) || found < 0 || found > 1_000_000_000 {
		return ontology.ErrArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[oid]
	if !ok {
		return ontology.ErrNotFound
	}
	l, lok := m.store.Get(loc)
	if !lok {
		return ontology.ErrNotFound
	}
	q, rok := m.ledger.Get(oid, loc)
	if !rok {
		return ontology.ErrNotFound
	}
	if o.status != Allocated && o.status != Short {
		return ontology.ErrState
	}
	if l.Locked {
		return ontology.ErrState
	}
	if found >= q {
		return ontology.ErrQuantity
	}
	// 先删本单记录，再删该库位上其余订单的全部记录。
	m.ledger.Remove(oid, loc)
	affected, others := m.ledger.RemoveAllAt(loc)
	touched := others + 1
	m.lastTouched = touched
	if touched > m.maxTouched {
		m.maxTouched = touched
	}
	reservedQty := q
	for _, dq := range affected {
		reservedQty += dq
	}
	m.store.ShortPick(loc, found, reservedQty)

	// 重分配次序：本单差额最先，其后按（priority 降序，订单号字节序）。
	type deficit struct {
		ord *order
		qty int64
	}
	defs := []deficit{{ord: o, qty: q - found}}
	for otherID, dq := range affected {
		if otherID != oid {
			defs = append(defs, deficit{ord: m.orders[otherID], qty: dq})
		}
	}
	rest := defs[1:]
	sort.SliceStable(rest, func(i, j int) bool {
		if rest[i].ord.priority != rest[j].ord.priority {
			return rest[i].ord.priority > rest[j].ord.priority
		}
		return rest[i].ord.id < rest[j].ord.id
	})
	defs = append(defs[:1], rest...)

	recomputeSet := map[ontology.ID]*order{o.id: o}
	for _, d := range defs {
		tentative := map[ontology.ID]int64{}
		sku := l.SKU
		_, still := m.allocFor(d.ord.id, sku, d.qty, tentative)
		if still != 0 {
			// 重分配失败：撤销本笔试探，整笔记缺口；其他预占不动。
			for rloc, rq := range tentative {
				m.ledger.Add(d.ord.id, rloc, -rq)
				m.store.Release(rloc, rq)
			}
			d.ord.shortfall += d.qty
		}
		recomputeSet[d.ord.id] = d.ord
	}
	for _, ro := range recomputeSet {
		m.recompute(ro)
	}
	return nil
}

// recompute 依据剩余预占记录与缺口重算订单状态。
// 无剩余记录：缺口为 0 置 Done，否则 Short；有剩余记录置 Short（有缺口）或 Allocated。
func (m *Manager) recompute(o *order) {
	has := len(m.ledger.LocsOf(o.id)) > 0
	switch {
	case !has && o.shortfall == 0:
		o.status = Done
	case o.shortfall > 0:
		o.status = Short
	default:
		o.status = Allocated
	}
}

// Unlock 把已锁定库位的 onHand 置为 counted 并解锁。
func (m *Manager) Unlock(loc ontology.ID, counted int64) error {
	if !ontology.ValidID(loc) || counted < 0 || counted > 1_000_000_000 {
		return ontology.ErrArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.store.Get(loc); !ok {
		return ontology.ErrNotFound
	}
	return m.store.Unlock(loc, counted)
}

// Cancel 仅对 Allocated 订单有效：释放全部未拣预占并置为 Cancelled。
func (m *Manager) Cancel(id ontology.ID) error {
	if !ontology.ValidID(id) {
		return ontology.ErrArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[id]
	if !ok {
		return ontology.ErrNotFound
	}
	if o.status != Allocated {
		return ontology.ErrState
	}
	locs := m.ledger.LocsOf(id)
	for loc, q := range locs {
		m.ledger.Remove(id, loc)
		m.store.Release(loc, q)
	}
	o.status = Cancelled
	return nil
}

func sumValues(m map[ontology.ID]int64) int64 {
	var s int64
	for _, v := range m {
		s += v
	}
	return s
}
