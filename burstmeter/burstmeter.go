// Package burstmeter 实现带封顶溢出、先偿欠额与到期计费的突发积分实例计量器。
package burstmeter

import (
	"errors"
	"sync"
)

// RejectReason 标识操作被拒绝的具体原因。
type RejectReason int

const (
	ReasonNone RejectReason = iota
	ReasonInvalidArgument
	ReasonSequenceFallback
	ReasonSequenceGap
)

// Mode 常量。
const (
	ModeStandard = 0
	ModeInfinite = 1
)

var (
	// ErrInvalidConfig 在任一构造参数越界时返回（配置非法，整体拒绝）。
	ErrInvalidConfig = errors.New("burstmeter: invalid config")
	// ErrInvalidArgument 在 Tick 的 m/u 或 SetMode 的 mode 越界时返回。
	ErrInvalidArgument = errors.New("burstmeter: invalid argument")
	// ErrSequenceFallback 在 Tick 的分钟序号小于下一个应处理序号时返回。
	ErrSequenceFallback = errors.New("burstmeter: sequence fallback")
	// ErrSequenceGap 在 Tick 的分钟序号大于下一个应处理序号时返回。
	ErrSequenceGap = errors.New("burstmeter: sequence gap")
)

// Batch 是一笔欠额批次：产生分钟 Minute 与剩余欠额 Remaining。
type Batch struct {
	Minute    int64
	Remaining int64
}

// TickResult 是单分钟 Tick 的输出。
type TickResult struct {
	Fee       int64 // 本分钟到期/切换产生的费用
	Balance   int64 // 本分钟结束后的余额 bal
	Debt      int64 // 本分钟结束后的未偿欠额总量
	Dropped   int64 // 本分钟因封顶丢弃的积分
	Throttled int64 // 本分钟被限速（无法满足）的消耗量
}

// Meter 是并发安全的突发积分计量器。
type Meter struct {
	mu sync.Mutex

	n     int64 // 核数
	b     int64 // 基线百分比
	cmax  int64 // 余额上限
	w     int64 // 结算窗口（分钟）
	price int64 // 每单位欠额价格
	dmax  int64 // 欠额总量上限

	bal int64 // 当前余额
	lb  int64 // 剩余启动积分（只被消耗）

	batches []Batch // 欠额批次队列，按产生分钟严格递增

	totalEarned   int64 // 累计入账
	totalConsumed int64 // 累计总消耗（x 之和中实际满足的部分，即 u1+u2 之和）
	totalRepaid   int64 // 累计入账用于偿还欠额的量
	totalDropped  int64 // 累计封顶丢弃量
	totalThrottle int64 // 累计被限速量
	totalFee      int64 // 累计计费

	next int64 // 下一个应处理的分钟序号
	mode int   // 当前模式
}

// Config 为计量器构造参数。
type Config struct {
	N           int64 // 核数，1..64
	B           int64 // 基线百分比，1..100
	Cmax        int64 // 余额上限，0..1e12
	L           int64 // 启动积分，0..Cmax
	W           int64 // 欠额结算窗口（分钟），1..1e6
	Price       int64 // 每单位欠额价格，0..1e6
	Dmax        int64 // 欠额总量上限，0..1e12
	InitialMode int   // 0 标准、1 无限
}

// New 校验配置并构造计量器；任一参数越界整体拒绝。
func New(c Config) (*Meter, error) {
	if c.N < 1 || c.N > 64 ||
		c.B < 1 || c.B > 100 ||
		c.Cmax < 0 || c.Cmax > 1_000_000_000_000 ||
		c.L < 0 || c.L > c.Cmax ||
		c.W < 1 || c.W > 1_000_000 ||
		c.Price < 0 || c.Price > 1_000_000 ||
		c.Dmax < 0 || c.Dmax > 1_000_000_000_000 ||
		(c.InitialMode != ModeStandard && c.InitialMode != ModeInfinite) {
		return nil, ErrInvalidConfig
	}
	return &Meter{
		n:     c.N,
		b:     c.B,
		cmax:  c.Cmax,
		w:     c.W,
		price: c.Price,
		dmax:  c.Dmax,
		lb:    c.L,
		next:  0,
		mode:  c.InitialMode,
	}, nil
}

// Tick 处理第 m 分钟（利用率 u 个百分点）。
//
// 拒绝顺序（只报第一个，被拒绝不改变任何状态）：
//  1. 参数非法：m<0 或 u 不在 [0,100]；
//  2. 序号回退：m < next；
//  3. 序号缺口：m > next。
//
// 每分钟严格按四步处理：到期计费 → 入账（先 FIFO 偿还欠额，余额再加 bal）→
// 消耗（先 lb 后 bal，无限模式下不足转欠额/限速）→ 封顶丢弃。
func (m *Meter) Tick(minute int64, utilization int) (TickResult, error) {
	if minute < 0 || utilization < 0 || utilization > 100 {
		return TickResult{}, ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if minute < m.next {
		return TickResult{}, ErrSequenceFallback
	}
	if minute > m.next {
		return TickResult{}, ErrSequenceGap
	}

	res := m.tickLocked(minute, int64(utilization))
	m.next = minute + 1
	return res, nil
}

// SetMode 切换模式；无限切标准时立即结算全部欠额。
func (m *Meter) SetMode(mode int) (int64, error) {
	if mode != ModeStandard && mode != ModeInfinite {
		return 0, ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if mode == m.mode {
		return 0, nil
	}

	var fee int64
	if m.mode == ModeInfinite && mode == ModeStandard {
		for _, batch := range m.batches {
			fee += batch.Remaining * m.price
		}
		m.totalFee += fee
		m.batches = m.batches[:0]
	}
	m.mode = mode
	return fee, nil
}

func (m *Meter) tickLocked(minute, u int64) TickResult {
	var (
		minuteFee      int64
		minuteDropped  int64
		minuteThrottle int64
	)

	// 第一步：到期计费。恰满足 m-产生分钟 >= W 即到期（先于入账）。
	kept := m.batches[:0]
	for _, batch := range m.batches {
		if minute-batch.Minute >= m.w {
			minuteFee += batch.Remaining * m.price
		} else {
			kept = append(kept, batch)
		}
	}
	m.batches = kept

	// 第二步：入账 e=n*b，先按 FIFO 偿还最旧批次，剩余加进 bal。
	earned := m.n * m.b
	credit := earned
	for i := range m.batches {
		if credit <= 0 {
			break
		}
		if credit >= m.batches[i].Remaining {
			credit -= m.batches[i].Remaining
			m.batches[i].Remaining = 0
		} else {
			m.batches[i].Remaining -= credit
			credit = 0
		}
	}
	repaid := earned - credit
	m.batches = compactPositive(m.batches)
	m.totalRepaid += repaid
	m.totalEarned += earned
	m.bal += credit

	// 第三步：消耗 x=n*u，先用 lb 再用 bal。
	need := m.n * u
	u1 := min64(m.lb, need)
	m.lb -= u1
	need -= u1
	u2 := min64(m.bal, need)
	m.bal -= u2
	need -= u2
	m.totalConsumed += u1 + u2

	// 无限模式：不足额转欠额批次（受 Dmax 截断），其余被限速。
	if need > 0 {
		if m.mode == ModeInfinite {
			room := m.dmax - m.debtLocked()
			borrow := min64(need, room)
			if borrow < 0 {
				borrow = 0
			}
			if borrow > 0 {
				m.batches = append(m.batches, Batch{Minute: minute, Remaining: borrow})
			}
			minuteThrottle = need - borrow
		} else {
			minuteThrottle = need
		}
	}
	m.totalThrottle += minuteThrottle

	// 第四步：封顶发生在消耗之后，超出 Cmax 的部分计入丢弃量。
	if m.bal > m.cmax {
		minuteDropped = m.bal - m.cmax
		m.bal = m.cmax
		m.totalDropped += minuteDropped
	}

	m.totalFee += minuteFee
	return TickResult{
		Fee:       minuteFee,
		Balance:   m.bal,
		Debt:      m.debtLocked(),
		Dropped:   minuteDropped,
		Throttled: minuteThrottle,
	}
}

func (m *Meter) debtLocked() int64 {
	var total int64
	for _, batch := range m.batches {
		total += batch.Remaining
	}
	return total
}

func compactPositive(batches []Batch) []Batch {
	out := batches[:0]
	for _, batch := range batches {
		if batch.Remaining > 0 {
			out = append(out, batch)
		}
	}
	return out
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// Balance 返回当前余额 bal。
func (m *Meter) Balance() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bal
}

// LaunchBalance 返回剩余启动积分 lb。
func (m *Meter) LaunchBalance() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lb
}

// Debt 返回未偿欠额总量。
func (m *Meter) Debt() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.debtLocked()
}

// Batches 返回欠额批次的快照（按产生分钟升序）。
func (m *Meter) Batches() []Batch {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Batch, len(m.batches))
	copy(out, m.batches)
	return out
}

// Next 返回下一个应处理的分钟序号。
func (m *Meter) Next() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.next
}

// Mode 返回当前模式。
func (m *Meter) Mode() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

// Totals 返回累计入账、消耗、偿还、丢弃、被限速与费用。
func (m *Meter) Totals() (earned, consumed, repaid, dropped, throttled, fee int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalEarned, m.totalConsumed, m.totalRepaid, m.totalDropped, m.totalThrottle, m.totalFee
}
