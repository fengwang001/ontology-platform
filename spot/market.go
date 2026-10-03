// Package spot 实现带统一出清价、中断免费小时与整点边界规则的现货容量市场。
//
// 市场维护一个请求集合，所有未终止请求按 (bid 降序, 提交序号升序) 排名，
// 排名前 K（当前容量）的请求构成运行集合，其余等待。出清价为第 K+1 名的
// bid，不足 K+1 个请求时为底价 Pmin。运行段按出清价逐小时计费：用户终止
// 的段按 ceil(时长/3600) 小时收费，市场中断的段按 floor(时长/3600) 小时
// 收费，第 k 小时（k 从 0 起）的价格为 price(段起点+3600k)。
package spot

import (
	"errors"
	"math"
	"math/big"
	"sort"
	"sync"
)

// HourSeconds 为一个计费小时的秒数，固定 3600。
const HourSeconds int64 = 3600

// 参数合法范围。
const (
	MinCapacity = 1
	MaxCapacity = 1000
	MinPrice    = 1
	MaxPrice    = 1_000_000_000
	MinID       = 0
	MaxID       = 1_000_000_000
	MinTime     = 0
	MaxTime     = 1_000_000_000_000_000
)

// 拒绝原因。各操作按题目规定的顺序只报第一个原因。
var (
	ErrInvalidConfig     = errors.New("spot: 配置非法")
	ErrInvalidArgument   = errors.New("spot: 参数非法")
	ErrClockRegression   = errors.New("spot: 时钟回退")
	ErrDuplicateID       = errors.New("spot: id 重复")
	ErrNotFound          = errors.New("spot: 请求不存在")
	ErrAlreadyTerminated = errors.New("spot: 请求已终止")
)

// EventKind 区分事件类型。
type EventKind int

const (
	// EventEnd 表示一个运行段结束。
	EventEnd EventKind = iota
	// EventStart 表示一个运行段开始。
	EventStart
)

// EndReason 区分运行段结束的计费口径。
type EndReason int

const (
	// ReasonUserTerminate 用户终止：按 ceil((e-s)/3600) 小时收费。
	ReasonUserTerminate EndReason = iota
	// ReasonMarketInterrupt 市场中断：按 floor((e-s)/3600) 小时收费。
	ReasonMarketInterrupt
)

// Event 为操作产生的事件。结束事件含该段费用 Fee。
type Event struct {
	Kind   EventKind
	ID     int64
	Reason EndReason // 仅结束事件有效
	Start  int64     // 仅结束事件有效：段起点
	End    int64     // 仅结束事件有效：段终点
	Fee    *big.Int  // 仅结束事件有效：该段费用
}

// request 为市场内部维护的请求状态。
type request struct {
	id         int64
	bid        int64
	seq        int64 // 提交序号，按成功的 Request 递增
	terminated bool
	running    bool
	segStart   int64
	bill       *big.Int // 全部已结束运行段费用之和
}

// pricePoint 为价格历史的一条记录 (t, 价)。
type pricePoint struct {
	t     int64
	price int64
}

// Market 为现货容量市场。所有方法可并发调用，
// 结果等价于某个串行顺序（内部以互斥锁串行化）。
type Market struct {
	mu       sync.Mutex
	capacity int
	floor    int64 // 底价 Pmin
	reqs     map[int64]*request
	seq      int64
	maxT     int64 // 已接受操作的最大 t
	history  []pricePoint
}

// NewMarket 构造市场。capacity 须在 [1,1000]，minPrice 须在 [1,1e9]，
// 越界时以 ErrInvalidConfig 整体拒绝。
func NewMarket(capacity int, minPrice int64) (*Market, error) {
	if capacity < MinCapacity || capacity > MaxCapacity ||
		minPrice < MinPrice || minPrice > MaxPrice {
		return nil, ErrInvalidConfig
	}
	return &Market{
		capacity: capacity,
		floor:    minPrice,
		reqs:     make(map[int64]*request),
		history:  []pricePoint{{t: 0, price: minPrice}},
	}, nil
}

// Request 提交一个持续请求。拒绝顺序：参数非法；时钟回退；id 重复（含已终止的）。
// 接受时在 t 时刻先加入请求，再重新计算排名、运行集合与出清价。
func (m *Market) Request(id, bid, t int64) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id < MinID || id > MaxID || bid < m.floor || bid > MaxPrice ||
		t < MinTime || t > MaxTime {
		return nil, ErrInvalidArgument
	}
	if t < m.maxT {
		return nil, ErrClockRegression
	}
	if _, ok := m.reqs[id]; ok {
		return nil, ErrDuplicateID
	}
	m.maxT = t
	m.reqs[id] = &request{id: id, bid: bid, seq: m.seq, bill: new(big.Int)}
	m.seq++
	ends, starts := m.recompute(t)
	return mergeEvents(ends, starts), nil
}

// Terminate 由用户终止请求。拒绝顺序：参数非法；时钟回退；不存在；已终止。
// 若请求正在运行，其运行段以「用户终止」口径结算；等待中的请求被终止不产生费用。
func (m *Market) Terminate(id, t int64) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id < MinID || id > MaxID || t < MinTime || t > MaxTime {
		return nil, ErrInvalidArgument
	}
	if t < m.maxT {
		return nil, ErrClockRegression
	}
	r, ok := m.reqs[id]
	if !ok {
		return nil, ErrNotFound
	}
	if r.terminated {
		return nil, ErrAlreadyTerminated
	}
	m.maxT = t
	r.terminated = true
	var ends []Event
	if r.running {
		fee := m.settle(r, t, ReasonUserTerminate)
		ends = append(ends, endEvent(r, t, fee, ReasonUserTerminate))
	}
	reEnds, starts := m.recompute(t)
	ends = append(ends, reEnds...)
	return mergeEvents(ends, starts), nil
}

// SetCapacity 把容量改为 k2。拒绝顺序：参数非法（k2 越界）；时钟回退。
// 接受时在 t 时刻先改容量，再重新计算排名、运行集合与出清价。
func (m *Market) SetCapacity(k2 int, t int64) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if k2 < MinCapacity || k2 > MaxCapacity || t < MinTime || t > MaxTime {
		return nil, ErrInvalidArgument
	}
	if t < m.maxT {
		return nil, ErrClockRegression
	}
	m.maxT = t
	m.capacity = k2
	ends, starts := m.recompute(t)
	return mergeEvents(ends, starts), nil
}

// Bill 返回该请求全部已结束运行段的费用之和；未知 id 返回 0。
func (m *Market) Bill(id int64) *big.Int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.reqs[id]; ok {
		return new(big.Int).Set(r.bill)
	}
	return new(big.Int)
}

// PriceAt 返回价格函数 price(x)：历史中时刻不大于 x 的最后一条的价。
func (m *Market) PriceAt(x int64) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.priceAt(x)
}

// RunningIDs 返回当前运行集合的 id（升序），用于校验不变量。
func (m *Market) RunningIDs() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []int64
	for _, r := range m.reqs {
		if r.running {
			ids = append(ids, r.id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// recompute 在操作已应用后重新计算排名、运行集合与出清价，
// 返回结束事件与开始事件（均未排序，由 mergeEvents 统一排序）。
func (m *Market) recompute(t int64) (ends, starts []Event) {
	active := make([]*request, 0, len(m.reqs))
	for _, r := range m.reqs {
		if !r.terminated {
			active = append(active, r)
		}
	}
	// 排名：bid 降序，提交序号升序。seq 唯一，故为全序，结果确定。
	sort.Slice(active, func(i, j int) bool {
		if active[i].bid != active[j].bid {
			return active[i].bid > active[j].bid
		}
		return active[i].seq < active[j].seq
	})

	inTopK := make(map[*request]bool, m.capacity)
	for i := 0; i < len(active) && i < m.capacity; i++ {
		inTopK[active[i]] = true
	}
	for _, r := range active {
		switch {
		case r.running && !inTopK[r]:
			fee := m.settle(r, t, ReasonMarketInterrupt)
			ends = append(ends, endEvent(r, t, fee, ReasonMarketInterrupt))
		case !r.running && inTopK[r]:
			r.running = true
			r.segStart = t
			starts = append(starts, Event{Kind: EventStart, ID: r.id})
		}
	}

	// 出清价：第 K+1 名的 bid，不足 K+1 个请求时为 Pmin。
	price := m.floor
	if len(active) > m.capacity {
		price = active[m.capacity].bid
	}
	if price != m.priceAt(t) {
		m.recordPrice(t, price)
	}
	return ends, starts
}

// settle 结束请求 r 当前运行段 [segStart, e)，按 reason 口径结算费用并累计。
func (m *Market) settle(r *request, e int64, reason EndReason) *big.Int {
	s := r.segStart
	r.running = false
	dur := e - s
	var hours int64
	if reason == ReasonUserTerminate {
		hours = ceilDiv(dur, HourSeconds)
	} else {
		hours = dur / HourSeconds
	}
	fee := m.feeFor(s, hours)
	r.bill.Add(r.bill, fee)
	return fee
}

// feeFor 计算 sum_{k=0}^{hours-1} price(s+3600*k)。
// 按价格历史区间分组统计，避免逐小时循环（时长可达 1e15 秒）。
func (m *Market) feeFor(s, hours int64) *big.Int {
	total := new(big.Int)
	if hours <= 0 {
		return total
	}
	for i, p := range m.history {
		a := p.t
		b := int64(math.MaxInt64)
		if i+1 < len(m.history) {
			b = m.history[i+1].t
		}
		// 小时起点为 s+3600k（k 取 [0, hours)），落在 [a, b) 内的个数：
		// k >= ceil((a-s)/3600) 且 k < ceil((b-s)/3600)。
		lo := ceilDiv(a-s, HourSeconds)
		var hi int64
		if b-s > hours*HourSeconds {
			hi = hours
		} else {
			hi = ceilDiv(b-s, HourSeconds)
		}
		if lo < 0 {
			lo = 0
		}
		if hi > hours {
			hi = hours
		}
		if cnt := hi - lo; cnt > 0 {
			total.Add(total, new(big.Int).Mul(
				big.NewInt(p.price), big.NewInt(cnt)))
		}
	}
	return total
}

// priceAt 返回 price(x)：历史中时刻不大于 x 的最后一条的价。
func (m *Market) priceAt(x int64) int64 {
	i := sort.Search(len(m.history), func(i int) bool {
		return m.history[i].t > x
	})
	if i == 0 {
		return m.floor
	}
	return m.history[i-1].price
}

// recordPrice 记录 (t, 价)；同一 t 的多次记录以最后一次为准。
func (m *Market) recordPrice(t, price int64) {
	if n := len(m.history); n > 0 && m.history[n-1].t == t {
		m.history[n-1].price = price
		return
	}
	m.history = append(m.history, pricePoint{t: t, price: price})
}

// endEvent 构造结束事件。
func endEvent(r *request, e int64, fee *big.Int, reason EndReason) Event {
	return Event{
		Kind:   EventEnd,
		ID:     r.id,
		Reason: reason,
		Start:  r.segStart,
		End:    e,
		Fee:    fee,
	}
}

// mergeEvents 汇总事件清单：先结束事件再开始事件，各自按 id 升序。
func mergeEvents(ends, starts []Event) []Event {
	sort.Slice(ends, func(i, j int) bool { return ends[i].ID < ends[j].ID })
	sort.Slice(starts, func(i, j int) bool { return starts[i].ID < starts[j].ID })
	return append(ends, starts...)
}

// ceilDiv 计算 ceil(x/d)，d > 0，x 可为负。
func ceilDiv(x, d int64) int64 {
	if x >= 0 {
		return (x + d - 1) / d
	}
	return -((-x) / d)
}
