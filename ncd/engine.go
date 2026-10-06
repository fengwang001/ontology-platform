// Package ncd 实现车险无赔款优惠（NCD）等级的续保定级引擎。
//
// 引擎由若干职责清晰的模块协作构成：
//   - engine.go  引擎装配：产品配置、全局时钟、被保人注册表、并发串行化；
//   - policy.go  保单年度记录：投保、续保、跨车转移、等级保护购买；
//   - claim.go   出险责任登记：出险登记、撤销、迟报归年；
//   - grade.go   等级升降裁定：单年度定级与迟报触发的追溯重定级、保费差额追补。
//
// 所有入口可在任意 goroutine 并发调用：引擎内部以单一互斥锁串行化，
// 效果等价于按锁获得顺序的某个串行执行，因此相同操作序列重放结果完全一致。
package ncd

import (
	"sort"
	"sync"
)

const (
	// YearDays 保单年度长度（天），年度为 [Start, Start+YearDays) 左闭右开。
	YearDays = 365
	// RenewalEarlyDays 续保窗口左端：原保单到期日前 30 天（含）起可办理续保。
	RenewalEarlyDays = 30
)

// Config 产品参数，登记（NewEngine）时给出。
type Config struct {
	MaxGrade           int     // 最高等级，正整数；等级取值 0..MaxGrade
	RenewalLateDays    int     // 续保窗口右端：到期日后 N 天（含），正整数
	LiabilityThreshold int     // 有责门槛，责任比例不小于该值（含相等）即为有责，0..100
	ProtectStartGrade  int     // 保护起始级，等级不低于该值方可购买保护，0..MaxGrade
	Premiums           []int64 // 各等级保费表，长度必须为 MaxGrade+1
}

func (c Config) validate() error {
	if c.MaxGrade < 1 {
		return errf(ErrInvalidParam, "最高级须为正整数: %d", c.MaxGrade)
	}
	if c.RenewalLateDays < 1 {
		return errf(ErrInvalidParam, "续保宽限天数须为正整数: %d", c.RenewalLateDays)
	}
	if c.LiabilityThreshold < 0 || c.LiabilityThreshold > 100 {
		return errf(ErrInvalidParam, "有责门槛须在 0..100: %d", c.LiabilityThreshold)
	}
	if c.ProtectStartGrade < 0 || c.ProtectStartGrade > c.MaxGrade {
		return errf(ErrInvalidParam, "保护起始级须在 0..%d: %d", c.MaxGrade, c.ProtectStartGrade)
	}
	if len(c.Premiums) != c.MaxGrade+1 {
		return errf(ErrInvalidParam, "保费表长度须为 %d，实际 %d", c.MaxGrade+1, len(c.Premiums))
	}
	return nil
}

// Engine 定级引擎。零值不可用，须由 NewEngine 构造。
type Engine struct {
	mu    sync.Mutex // 串行化全部入口，保证并发等价于某个串行顺序
	cfg   Config
	clock int // 当前时刻（整数天），只能前进

	insureds map[string]*Insured

	adjudications int64 // 定级函数执行次数，用于验证续保开销与历史长度无关
}

// Insured 被保人：保单年度历史、保单、出险登记与追补记录。
type Insured struct {
	Years    []*Year           // 保单年度，按 Start 递增、互不重叠
	Policies []*Policy         // 保单（含已终止），跨车转移时追加
	Claims   map[string]*Claim // 全部已登记出险，按事故编号索引
	Catchups []Catchup         // 追溯重定级产生的保费差额追补记录
}

// NewEngine 按产品配置创建引擎。
func NewEngine(cfg Config) (*Engine, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Engine{cfg: cfg, insureds: make(map[string]*Insured)}, nil
}

// AddInsured 登记被保人。重复登记报参数非法。
func (e *Engine) AddInsured(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" {
		return errf(ErrInvalidParam, "被保人编号为空")
	}
	if _, ok := e.insureds[id]; ok {
		return errf(ErrInvalidParam, "被保人已登记: %s", id)
	}
	e.insureds[id] = &Insured{Claims: make(map[string]*Claim)}
	return nil
}

// Advance 推进时钟到 day。回退报时钟回退。
func (e *Engine) Advance(day int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if day < 0 {
		return errf(ErrInvalidParam, "时刻须为非负整数: %d", day)
	}
	if err := e.checkClock(day); err != nil {
		return err
	}
	e.clock = day
	return nil
}

// Now 返回当前时刻。
func (e *Engine) Now() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

// AdjudicationCount 返回定级函数累计执行次数，用于性能验证。
func (e *Engine) AdjudicationCount() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.adjudications
}

// insured 按拒绝次序检查：被保人不存在。
func (e *Engine) insured(id string) (*Insured, *Error) {
	ins, ok := e.insureds[id]
	if !ok {
		return nil, errf(ErrInsuredNotFound, "被保人不存在: %s", id)
	}
	return ins, nil
}

// checkClock 按拒绝次序检查：时钟回退。
func (e *Engine) checkClock(day int) *Error {
	if day < e.clock {
		return errf(ErrClockBackward, "时钟回退: 当前 %d，请求 %d", e.clock, day)
	}
	return nil
}

// findYear 返回覆盖事故日 day 的保单年度下标，未承保返回 -1。
// 年度按 Start 递增且互不重叠，二分查找。
func findYear(ins *Insured, day int) int {
	i := sort.Search(len(ins.Years), func(i int) bool { return ins.Years[i].Start > day }) - 1
	if i >= 0 && day < ins.Years[i].End {
		return i
	}
	return -1
}

// activePolicyAt 返回 day 时刻在保（未终止且年度覆盖 day）的保单，无则 nil。
func activePolicyAt(ins *Insured, day int) *Policy {
	for _, p := range ins.Policies {
		if !p.Active {
			continue
		}
		y := ins.Years[p.YearIndex]
		if y.Start <= day && day < y.End {
			return p
		}
	}
	return nil
}
