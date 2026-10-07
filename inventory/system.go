package inventory

import (
	"fmt"
	"sort"
	"sync"
)

// System 是多仓库存承诺系统的外观（facade）。
//
// 并发模型：全部公开方法共用一把互斥锁，任何并发调用的结果都等价于
// 按锁获得顺序展开的某个串行顺序；在此串行顺序内，分配策略完全确定，
// 因此相同操作序列的重放得到完全相同的发货仓分配。
type System struct {
	mu sync.Mutex
	// clock 为当前时刻，等于上一次被接受操作携带的时刻；被拒绝的操作
	// 不改变时钟。查询类操作不推进时钟。
	clock int64
	// whs 按仓库号索引；sorted 为按优先序号升序排列的同一批仓库，
	// 承诺规划只遍历 sorted，保证确定性。
	whs    map[string]*Warehouse
	sorted []*Warehouse
	// orders 为订单号 -> 预留记录集合。已到期预留的记录保留在索引中，
	// 以便确认出库时能区分“预留已过期”与“订单不存在”；它们不参与
	// ATP 计算（失效预留已从各桶的堆中物理移除）。
	orders map[string]*orderReservations
	m      metrics
}

// orderReservations 是一张某订单的全部预留记录及其共同到期时刻。
type orderReservations struct {
	expireAt int64
	recs     []*reservation
}

// NewSystem 创建一个空系统。
func NewSystem() *System {
	return &System{
		whs:    make(map[string]*Warehouse),
		orders: make(map[string]*orderReservations),
	}
}

// Clock 返回当前时刻（上一次被接受操作携带的时刻）。
func (s *System) Clock() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

// Stats 返回运行统计快照（只读，不推进时钟）。
func (s *System) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{ReservationsExamined: s.m.reservationsExamined}
}

// ResetStats 将运行统计清零，用于分段测量。
func (s *System) ResetStats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m = metrics{}
}

// AddWarehouse 注册仓库。优先序号全局唯一，小者优先。
// 注册属于配置而非业务操作，不携带时刻、不推进时钟。
func (s *System) AddWarehouse(id string, priority int) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || priority < 0 {
		return newError(ReasonInvalidParam, -1, "仓库号为空或优先序号为负")
	}
	if _, ok := s.whs[id]; ok {
		return newError(ReasonInvalidParam, -1, "仓库号重复："+id)
	}
	for _, w := range s.sorted {
		if w.priority == priority {
			return newError(ReasonInvalidParam, -1, fmt.Sprintf("优先序号 %d 重复", priority))
		}
	}
	w := newWarehouse(id, priority)
	s.whs[id] = w
	s.sorted = append(s.sorted, w)
	sort.Slice(s.sorted, func(i, j int) bool { return s.sorted[i].priority < s.sorted[j].priority })
	return nil
}

// checkClock 校验操作携带的时刻不回退；调用前须已持锁。
func (s *System) checkClock(now int64) *Error {
	if now < s.clock {
		return newError(ReasonClockRollback, -1,
			fmt.Sprintf("操作时刻 %d 小于当前时刻 %d", now, s.clock))
	}
	return nil
}

// warehouse 返回已注册仓库，未注册时报参数非法；调用前须已持锁。
func (s *System) warehouse(id string) (*Warehouse, *Error) {
	w, ok := s.whs[id]
	if !ok {
		return nil, newError(ReasonInvalidParam, -1, "仓库未注册："+id)
	}
	return w, nil
}

// AddOnHand 增加某仓某商品的现货。携带时刻 now，被接受后时钟推进到 now。
func (s *System) AddOnHand(warehouse, product string, qty, now int64) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := s.warehouse(warehouse)
	if err != nil {
		return err
	}
	if product == "" || qty <= 0 || now < 0 {
		return newError(ReasonInvalidParam, -1, "商品为空、数量非正或时刻为负")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	w.bucket(product).onHand += qty
	s.clock = now
	return nil
}

// AddPlannedInbound 登记一条计划入库（到货时刻、数量）。
// 入库单号在同一（仓库，商品）桶内唯一。
func (s *System) AddPlannedInbound(warehouse, product, inboundID string, arrivalAt, qty, now int64) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := s.warehouse(warehouse)
	if err != nil {
		return err
	}
	if product == "" || inboundID == "" || arrivalAt < 0 || qty <= 0 || now < 0 {
		return newError(ReasonInvalidParam, -1, "商品或入库单号为空、时刻为负或数量非正")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	b := w.bucket(product)
	if _, ok := b.inbounds[inboundID]; ok {
		return newError(ReasonInvalidParam, -1, "入库单号重复："+inboundID)
	}
	b.inbounds[inboundID] = inbound{id: inboundID, arrivalAt: arrivalAt, qty: qty}
	s.clock = now
	return nil
}

// ConfirmArrival 确认计划入库到货：数量转为现货，且不再作为计划入库计入，
// 因此不会被重复计入。只允许对尚未确认到货的入库单进行。
func (s *System) ConfirmArrival(warehouse, product, inboundID string, now int64) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := s.warehouse(warehouse)
	if err != nil {
		return err
	}
	if inboundID == "" || now < 0 {
		return newError(ReasonInvalidParam, -1, "入库单号为空或时刻为负")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	b := w.findBucket(product)
	if b == nil {
		return newError(ReasonInboundNotFound, -1, "入库单不存在："+inboundID)
	}
	in, ok := b.inbounds[inboundID]
	if !ok {
		return newError(ReasonInboundNotFound, -1, "入库单不存在或已到货："+inboundID)
	}
	delete(b.inbounds, inboundID)
	b.onHand += in.qty
	s.clock = now
	return nil
}

// CommitOrder 承诺一张订单：整单全有或全无。
//
// 参数：订单号、订单行（按给定次序处理）、承诺时刻（不得早于当前时刻）、
// 是否允许拆分、预留时长、拆分涉及仓库数上限。
// 成功后各用到的仓库为该订单建立预留，到期时刻为承诺时刻加预留时长。
func (s *System) CommitOrder(orderID string, lines []OrderLine, commitAt int64, allowSplit bool, reserveFor int64, maxWarehouses int) (*CommitResult, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 拒绝优先级 1：参数非法。
	if err := validateCommit(orderID, lines, commitAt, reserveFor, maxWarehouses); err != nil {
		return nil, err
	}
	// 拒绝优先级 2：时钟回退（承诺时刻即本操作携带的时刻）。
	if err := s.checkClock(commitAt); err != nil {
		return nil, err
	}
	// 拒绝优先级 3：订单重复（同一订单号已存在仍有效的预留）。
	if ord, ok := s.orders[orderID]; ok && commitAt < ord.expireAt {
		return nil, newError(ReasonDuplicateOrder, -1, "订单号已存在有效预留："+orderID)
	}
	// 拒绝优先级 4/5：永久/暂时缺货（按下标最小的缺货行报告）。
	allocs, failLine, permanent := s.plan(lines, commitAt, allowSplit)
	if failLine >= 0 {
		reason := ReasonTemporaryShortage
		if permanent {
			reason = ReasonPermanentShortage
		}
		return nil, newError(reason, failLine,
			fmt.Sprintf("商品 %s 数量 %d 无法满足", lines[failLine].Product, lines[failLine].Qty))
	}
	// 拒绝优先级 6：拆分过多。贪心取用方案固定，不为凑上限改换方案。
	if n := distinctWarehouses(allocs); n > maxWarehouses {
		return nil, newError(ReasonTooManySplits, -1,
			fmt.Sprintf("整单涉及 %d 个仓库，超过上限 %d", n, maxWarehouses))
	}

	// 接受：建立预留、推进时钟。
	expireAt := commitAt + reserveFor
	recs := make([]*reservation, 0, len(allocs))
	for _, a := range allocs {
		r := &reservation{orderID: orderID, product: a.Product, qty: a.Qty, expireAt: expireAt}
		s.whs[a.Warehouse].bucket(a.Product).addReservation(r)
		recs = append(recs, r)
	}
	s.orders[orderID] = &orderReservations{expireAt: expireAt, recs: recs}
	s.clock = commitAt
	return &CommitResult{OrderID: orderID, ExpireAt: expireAt, Allocations: allocs}, nil
}

func validateCommit(orderID string, lines []OrderLine, commitAt, reserveFor int64, maxWarehouses int) *Error {
	if orderID == "" {
		return newError(ReasonInvalidParam, -1, "订单号为空")
	}
	if len(lines) == 0 {
		return newError(ReasonInvalidParam, -1, "订单行为空")
	}
	if commitAt < 0 || reserveFor < 0 {
		return newError(ReasonInvalidParam, -1, "承诺时刻或预留时长为负")
	}
	if maxWarehouses < 1 {
		return newError(ReasonInvalidParam, -1, "仓库数上限小于 1")
	}
	for i, ln := range lines {
		if ln.Product == "" || ln.Qty <= 0 {
			return newError(ReasonInvalidParam, -1,
				fmt.Sprintf("订单行 %d 商品为空或数量非正", i))
		}
	}
	return nil
}

func distinctWarehouses(allocs []Allocation) int {
	set := make(map[string]struct{}, len(allocs))
	for _, a := range allocs {
		set[a.Warehouse] = struct{}{}
	}
	return len(set)
}

// ConfirmOutbound 确认出库：把该订单仍有效的预留转为现货扣减。
// 已到期的预留不可确认（报预留已过期）；订单不存在报订单不存在。
func (s *System) ConfirmOutbound(orderID string, now int64) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if orderID == "" || now < 0 {
		return newError(ReasonInvalidParam, -1, "订单号为空或时刻为负")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	ord, ok := s.orders[orderID]
	if !ok {
		return newError(ReasonOrderNotFound, -1, "订单不存在："+orderID)
	}
	if now >= ord.expireAt {
		return newError(ReasonReservationExpired, -1, "预留已到期，不可确认出库："+orderID)
	}
	// 先校验后落账：任一仓现货不足则整单拒绝，不改变任何状态。
	for _, r := range ord.recs {
		if r.bucket.onHand < r.qty {
			return newError(ReasonInsufficientOnHand, -1,
				fmt.Sprintf("订单 %s 商品 %s 现货 %d 不足 %d（预留可能由未到货的计划入库支撑）",
					orderID, r.product, r.bucket.onHand, r.qty))
		}
	}
	for _, r := range ord.recs {
		r.bucket.onHand -= r.qty
		r.bucket.removeReservation(r)
	}
	delete(s.orders, orderID)
	s.clock = now
	return nil
}

// ReleaseReservation 释放该订单仍有效的预留。对已到期或不存在的订单
// 一律报订单不存在（与订单号重复可区分）。
func (s *System) ReleaseReservation(orderID string, now int64) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if orderID == "" || now < 0 {
		return newError(ReasonInvalidParam, -1, "订单号为空或时刻为负")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	ord, ok := s.orders[orderID]
	if !ok || now >= ord.expireAt {
		return newError(ReasonOrderNotFound, -1, "订单不存在或预留已到期："+orderID)
	}
	for _, r := range ord.recs {
		r.bucket.removeReservation(r)
	}
	delete(s.orders, orderID)
	s.clock = now
	return nil
}

// ATP 查询某仓某商品在给定承诺时刻的可承诺量。
// 只读：不推进时钟，但以当前时钟判定预留是否有效；
// 承诺时刻早于当前时刻时报参数非法。
func (s *System) ATP(warehouse, product string, at int64) (int64, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := s.warehouse(warehouse)
	if err != nil {
		return 0, err
	}
	if product == "" || at < 0 {
		return 0, newError(ReasonInvalidParam, -1, "商品为空或时刻为负")
	}
	if at < s.clock {
		return 0, newError(ReasonInvalidParam, -1,
			fmt.Sprintf("查询时刻 %d 早于当前时刻 %d", at, s.clock))
	}
	b := w.findBucket(product)
	if b == nil {
		return 0, nil
	}
	return b.atp(at, s.clock, &s.m), nil
}

// ReservationDetails 查询某订单的预留明细。只读，不推进时钟；
// 有效性以当前时钟判定。订单无任何记录时报订单不存在。
func (s *System) ReservationDetails(orderID string) ([]ReservationDetail, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if orderID == "" {
		return nil, newError(ReasonInvalidParam, -1, "订单号为空")
	}
	ord, ok := s.orders[orderID]
	if !ok {
		return nil, newError(ReasonOrderNotFound, -1, "订单不存在："+orderID)
	}
	valid := s.clock < ord.expireAt
	details := make([]ReservationDetail, 0, len(ord.recs))
	for _, r := range ord.recs {
		details = append(details, ReservationDetail{
			Warehouse: warehouseIDOf(r),
			Product:   r.product,
			Qty:       r.qty,
			ExpireAt:  r.expireAt,
			Valid:     valid,
		})
	}
	return details, nil
}

// warehouseIDOf 反查预留所属仓库号。预留对象不存仓库号，
// 通过桶反查在系统层完成（见 system 内的 bucketOwner 索引）。
func warehouseIDOf(r *reservation) string {
	return r.bucket.owner
}

// Validate 校验系统不变量，供测试与运行期自检：
//   - 任何仓库任何商品的现货不得为负；
//   - 当前时钟下仍有效的预留之和不得超过当时的可承诺上限；
//   - 预留堆内数量之和与增量维护的 reserved 一致。
func (s *System) Validate() *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.sorted {
		for product, b := range w.buckets {
			if b.onHand < 0 {
				return newError(ReasonInvalidParam, -1,
					fmt.Sprintf("不变量破坏：%s/%s 现货为负 %d", w.id, product, b.onHand))
			}
			b.purgeExpired(s.clock, &s.m)
			var heapSum int64
			for _, r := range b.res {
				heapSum += r.qty
			}
			if heapSum != b.reserved {
				return newError(ReasonInvalidParam, -1,
					fmt.Sprintf("不变量破坏：%s/%s 预留堆和 %d 与记账 %d 不一致", w.id, product, heapSum, b.reserved))
			}
			cap := b.onHand
			for _, in := range b.inbounds {
				if in.arrivalAt <= s.clock {
					cap += in.qty
				}
			}
			if b.reserved > cap {
				return newError(ReasonInvalidParam, -1,
					fmt.Sprintf("不变量破坏：%s/%s 有效预留 %d 超过可承诺上限 %d", w.id, product, b.reserved, cap))
			}
		}
	}
	return nil
}
