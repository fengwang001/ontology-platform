package inventory

import (
	"sort"
	"sync"
)

// orderEntry 是订单索引：一个订单号对应一组预留记录与统一的到期时刻。
// 已到期/已释放/已出库的订单条目处理方式见各操作注释。
type orderEntry struct {
	expiry int64
	recs   []*Reservation
}

type warehouse struct {
	id       string
	priority int
	buckets  map[string]*bucket // 商品 -> 库存桶
}

// System 是电商多仓库存的可承诺量查询与订单承诺系统。
// 所有公开方法可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序。
type System struct {
	mu     sync.Mutex
	clock  int64
	whs    map[string]*warehouse
	sorted []*warehouse // 按 (优先序号, 仓库号) 升序，保证分配确定性
	orders map[string]*orderEntry
	// examined 统计懒清理考察过的预留记录总数，用于证明
	// 单行承诺判定的考察次数不随历史订单数或已失效预留数增长。
	examined int64
}

func NewSystem() *System {
	return &System{
		whs:    make(map[string]*warehouse),
		orders: make(map[string]*orderEntry),
	}
}

// Clock 返回当前时刻，即上一次被接受操作携带的时刻。
func (s *System) Clock() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

// ExaminedReservations 返回懒清理累计考察的预留记录数（性能证明指标）。
func (s *System) ExaminedReservations() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.examined
}

// checkClock 校验操作时刻不得早于当前时钟，被拒绝时不改变任何状态。
func (s *System) checkClock(t int64) *Error {
	if t < s.clock {
		return newError(CodeClockRollback, "时钟回退：操作时刻 %d 早于当前时刻 %d", t, s.clock)
	}
	return nil
}

func (s *System) bucketOf(whID, product string) *bucket {
	w := s.whs[whID]
	if w == nil {
		return nil
	}
	return w.buckets[product]
}

// bucketForWrite 返回桶，不存在则创建（仅写操作使用）。
func (s *System) bucketForWrite(whID, product string) *bucket {
	w := s.whs[whID]
	b := w.buckets[product]
	if b == nil {
		b = newBucket()
		w.buckets[product] = b
	}
	return b
}

// AddWarehouse 注册仓库，优先序号小者发货优先；序号相同按仓库号字典序保证确定性。
func (s *System) AddWarehouse(id string, priority int, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || priority < 0 || t < 0 {
		return newError(CodeInvalidParam, "参数非法：仓库号为空、优先序号或时刻为负")
	}
	if _, dup := s.whs[id]; dup {
		return newError(CodeInvalidParam, "参数非法：仓库 %s 已存在", id)
	}
	if err := s.checkClock(t); err != nil {
		return err
	}
	w := &warehouse{id: id, priority: priority, buckets: make(map[string]*bucket)}
	s.whs[id] = w
	s.sorted = append(s.sorted, w)
	sort.Slice(s.sorted, func(i, j int) bool {
		if s.sorted[i].priority != s.sorted[j].priority {
			return s.sorted[i].priority < s.sorted[j].priority
		}
		return s.sorted[i].id < s.sorted[j].id
	})
	s.clock = t
	return nil
}

// AddStock 增加某仓某商品的现货量。
func (s *System) AddStock(wh, product string, qty, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkStockArgs(wh, product, qty, t); err != nil {
		return err
	}
	if err := s.checkClock(t); err != nil {
		return err
	}
	s.bucketForWrite(wh, product).onHand += qty
	s.clock = t
	return nil
}

// AddInbound 登记一条计划入库（到货时刻、数量）。
func (s *System) AddInbound(wh, product string, qty, arrival, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkStockArgs(wh, product, qty, t); err != nil {
		return err
	}
	if arrival < 0 {
		return newError(CodeInvalidParam, "参数非法：到货时刻 %d 为负", arrival)
	}
	if err := s.checkClock(t); err != nil {
		return err
	}
	b := s.bucketForWrite(wh, product)
	b.inbound[arrival] += qty
	b.inboundTotal += qty
	s.clock = t
	return nil
}

// ConfirmArrival 确认到货：把指定到货时刻、尚未确认的计划入库转为现货。
// 对不存在或已确认的入库报 CodeInboundNotFound，转换只发生一次，不会重复计入。
func (s *System) ConfirmArrival(wh, product string, arrival, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if wh == "" || product == "" || arrival < 0 || t < 0 {
		return newError(CodeInvalidParam, "参数非法")
	}
	if _, ok := s.whs[wh]; !ok {
		return newError(CodeInvalidParam, "参数非法：仓库 %s 不存在", wh)
	}
	if err := s.checkClock(t); err != nil {
		return err
	}
	b := s.bucketOf(wh, product)
	if b == nil || b.inbound[arrival] <= 0 {
		return newError(CodeInboundNotFound, "仓库 %s 商品 %s 没有到货时刻为 %d 的待到货入库", wh, product, arrival)
	}
	b.onHand += b.inbound[arrival]
	b.inboundTotal -= b.inbound[arrival]
	delete(b.inbound, arrival)
	s.clock = t
	return nil
}

func (s *System) checkStockArgs(wh, product string, qty, t int64) *Error {
	if wh == "" || product == "" || qty <= 0 || t < 0 {
		return newError(CodeInvalidParam, "参数非法：仓库/商品为空、数量非正或时刻为负")
	}
	if _, ok := s.whs[wh]; !ok {
		return newError(CodeInvalidParam, "参数非法：仓库 %s 不存在", wh)
	}
	return nil
}

// QueryATP 只读查询某仓某商品在给定承诺时刻的可承诺量。
// 不推进时钟，以当前时钟判定预留是否有效；承诺时刻早于当前时刻报参数非法。
func (s *System) QueryATP(wh, product string, commitTime int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if wh == "" || product == "" || commitTime < 0 {
		return 0, newError(CodeInvalidParam, "参数非法")
	}
	if _, ok := s.whs[wh]; !ok {
		return 0, newError(CodeInvalidParam, "参数非法：仓库 %s 不存在", wh)
	}
	if commitTime < s.clock {
		return 0, newError(CodeInvalidParam, "参数非法：承诺时刻 %d 早于当前时刻 %d", commitTime, s.clock)
	}
	b := s.bucketOf(wh, product)
	if b == nil {
		return 0, nil
	}
	return b.onHand + b.inboundArrivedBy(commitTime) - b.validReservedNow(s.clock), nil
}

// ReservationInfo 是订单预留明细的只读视图。
type ReservationInfo struct {
	Warehouse string
	Product   string
	Qty       int64
	Expiry    int64
	Valid     bool // 以当前时钟判定：当前时刻严格小于到期时刻才有效
}

// QueryOrder 只读查询某订单的预留明细；订单不存在时 found 为 false。
func (s *System) QueryOrder(orderID string) (infos []ReservationInfo, found bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.orders[orderID]
	if !ok {
		return nil, false
	}
	infos = make([]ReservationInfo, 0, len(e.recs))
	for _, r := range e.recs {
		infos = append(infos, ReservationInfo{
			Warehouse: r.Warehouse,
			Product:   r.Product,
			Qty:       r.Qty,
			Expiry:    r.Expiry,
			Valid:     r.alive && r.Expiry > s.clock,
		})
	}
	return infos, true
}

// bucketState 返回桶的内部状态快照，供测试校验不变量。
func (s *System) bucketState(wh, product string) (onHand, pendingInbound, reserved int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bucketOf(wh, product)
	if b == nil {
		return 0, 0, 0
	}
	return b.onHand, b.inboundTotal, b.reserved
}
