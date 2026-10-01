// Package quota 实现分周期配额结转账本。
//
// 账本把跨周期的用量记录按时间比例摊入各周期，并把未用额度按上限
// 结转到下一周期。查询时按当前全部记录现算，因此结果与记录到达
// 顺序无关，晚到的记录会改变此前周期的结果并顺延影响之后的结转。
package quota

import (
	"errors"
	"sync"
)

// 可区分的拒绝原因。
var (
	ErrNonPositiveQuota  = errors.New("quota: 每周期基础额度 Q 必须为正")
	ErrNonPositivePeriod = errors.New("quota: 周期长度 P 必须为正")
	ErrNegativeMaxCarry  = errors.New("quota: 结转上限 M 不能为负")

	ErrStartBeforeT0   = errors.New("quota: 记录起点 s 早于账本起点 t0")
	ErrInvalidInterval = errors.New("quota: 记录区间非法，需满足 e > s")
	ErrNegativeAmount  = errors.New("quota: 记录总量 amount 不能为负")
	ErrDuplicateID     = errors.New("quota: 记录标识重复")

	ErrNegativePeriodIndex = errors.New("quota: 查询周期不能为负")
)

// Config 是账本创建参数。
type Config struct {
	T0       int64 // 起点：第 0 个周期从 T0 开始
	Period   int64 // 周期长度 P，必须为正
	Quota    int64 // 每周期基础额度 Q，必须为正
	MaxCarry int64 // 结转上限 M，必须非负
}

// Record 是一条用量记录，占用半开区间 [Start, End)，总量为 Amount。
type Record struct {
	ID     string
	Start  int64
	End    int64
	Amount int64
}

// Report 是单个周期的查询结果。
type Report struct {
	Usage     int64 // 本周期摊入的总用量
	Effective int64 // 有效额度 = Quota + CarryIn
	CarryIn   int64 // 从上一周期结转进来的额度（第 0 周期为 0）
	Overage   int64 // 超额量 = max(0, Usage-Effective)，只报告不拒绝
}

// Ledger 是分周期配额结转账本，可并发调用。
type Ledger struct {
	mu      sync.RWMutex
	cfg     Config
	records map[string]Record
}

// NewLedger 创建账本；参数非法时整体拒绝并返回可区分的原因。
func NewLedger(cfg Config) (*Ledger, error) {
	if cfg.Quota <= 0 {
		return nil, ErrNonPositiveQuota
	}
	if cfg.Period <= 0 {
		return nil, ErrNonPositivePeriod
	}
	if cfg.MaxCarry < 0 {
		return nil, ErrNegativeMaxCarry
	}
	return &Ledger{cfg: cfg, records: make(map[string]Record)}, nil
}

// AddRecord 添加一条用量记录；非法时整体拒绝且不改变任何账目。
// 多重原因同时成立时按「s 早于 t0、区间非法、amount 为负、标识重复」
// 的顺序只报第一个。
func (l *Ledger) AddRecord(r Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.Start < l.cfg.T0 {
		return ErrStartBeforeT0
	}
	if r.End <= r.Start {
		return ErrInvalidInterval
	}
	if r.Amount < 0 {
		return ErrNegativeAmount
	}
	if _, ok := l.records[r.ID]; ok {
		return ErrDuplicateID
	}
	l.records[r.ID] = r
	return nil
}

// Query 查询第 k 个周期，按当前全部记录现算。
func (l *Ledger) Query(k int64) (Report, error) {
	if k < 0 {
		return Report{}, ErrNegativePeriodIndex
	}
	l.mu.RLock()
	defer l.mu.RUnlock()

	usage := make([]int64, k+1)
	for _, r := range l.records {
		for period, share := range l.allocate(r) {
			if period <= k {
				usage[period] += share
			}
		}
	}

	var rep Report
	var carry int64
	for period := int64(0); period <= k; period++ {
		eff := l.cfg.Quota + carry
		rep = Report{Usage: usage[period], Effective: eff, CarryIn: carry}
		if rep.Usage > eff {
			rep.Overage = rep.Usage - eff
		}
		unused := eff - rep.Usage
		if unused < 0 {
			unused = 0
		}
		carry = min(unused, l.cfg.MaxCarry)
	}
	return rep, nil
}

// allocate 把一条记录按与各周期的重叠长度摊入各周期：
// 每周期分得 Amount*重叠长度/区间总长 向下取整，余数全部加到
// 区间所触及的最后一个周期。各周期摊入量之和恒等于 Amount。
func (l *Ledger) allocate(r Record) map[int64]int64 {
	first := (r.Start - l.cfg.T0) / l.cfg.Period
	last := (r.End - 1 - l.cfg.T0) / l.cfg.Period
	length := r.End - r.Start

	shares := make(map[int64]int64, last-first+1)
	var sum int64
	for period := first; period <= last; period++ {
		lo := max(r.Start, l.cfg.T0+period*l.cfg.Period)
		hi := min(r.End, l.cfg.T0+(period+1)*l.cfg.Period)
		share := r.Amount * (hi - lo) / length
		shares[period] = share
		sum += share
	}
	shares[last] += r.Amount - sum
	return shares
}
