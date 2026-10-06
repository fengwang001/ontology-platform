// Package ncd 实现车险无赔款优惠（NCD）等级的续保定级引擎。
//
// 引擎由四部分协作构成：保单年度记录（policy.go）、出险责任登记
// （claim.go）、等级升降裁定与保护（policy.go 的 grade/BuyProtection）、
// 追溯重定级与追补（claim.go 的 regrade 触发点）。所有入口可并发调用，
// 内部以单一互斥锁串行化，结果等价于某个串行顺序。
package ncd

import "sync"

// Config 为登记时给出的业务参数，创建引擎后不可变。
type Config struct {
	MaxLevel           int   // 最高等级（正整数），等级取值 0..MaxLevel
	RenewalLateDays    int   // N：到期日后 N 天（含）内办理仍视为连续续保
	LiabilityThreshold int   // 有责门槛：责任比例 >= 该值记为有责（0..100）
	ProtectMinLevel    int   // 保护起始级：等级不低于该值方可购买保护
	Premiums           []int // 各等级保费表，长度须为 MaxLevel+1
}

// Surcharge 为一次追溯重定级产生的保费差额追补记录。
type Surcharge struct {
	YearStart int // 被重定级的保单年度起日
	OldLevel  int
	NewLevel  int
	Amount    int // Premiums[NewLevel] - Premiums[OldLevel]
}

// YearInfo 是保单年度的只读快照，供查询与测试。
type YearInfo struct {
	Start     int
	End       int
	Level     int
	Claims    int
	Protected bool
}

// Engine 为续保定级引擎，零值不可用，须由 NewEngine 创建。
type Engine struct {
	mu         sync.Mutex
	cfg        Config
	clock      int
	insureds   map[string]*insured
	gradeSteps int // 定级计算次数，用于验证续保 O(1) 与重定级范围
}

// NewEngine 校验登记参数并创建引擎。
func NewEngine(cfg Config) (*Engine, error) {
	const op = "NewEngine"
	if cfg.MaxLevel < 1 {
		return nil, newErr(op, ErrInvalidParam, "最高等级须为正整数: %d", cfg.MaxLevel)
	}
	if cfg.RenewalLateDays < 1 {
		return nil, newErr(op, ErrInvalidParam, "续保宽限天数须为正整数: %d", cfg.RenewalLateDays)
	}
	if cfg.LiabilityThreshold < 0 || cfg.LiabilityThreshold > 100 {
		return nil, newErr(op, ErrInvalidParam, "有责门槛须在 0..100: %d", cfg.LiabilityThreshold)
	}
	if cfg.ProtectMinLevel < 0 || cfg.ProtectMinLevel > cfg.MaxLevel {
		return nil, newErr(op, ErrInvalidParam, "保护起始级须在 0..%d: %d", cfg.MaxLevel, cfg.ProtectMinLevel)
	}
	if len(cfg.Premiums) != cfg.MaxLevel+1 {
		return nil, newErr(op, ErrInvalidParam, "保费表长度须为 %d，实际 %d", cfg.MaxLevel+1, len(cfg.Premiums))
	}
	for lvl, p := range cfg.Premiums {
		if p < 0 {
			return nil, newErr(op, ErrInvalidParam, "等级 %d 保费为负: %d", lvl, p)
		}
	}
	return &Engine{cfg: cfg, insureds: make(map[string]*insured)}, nil
}

// Clock 返回当前时刻（整数天）。
func (e *Engine) Clock() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

// GradeSteps 返回累计定级计算次数，用于性能验证。
func (e *Engine) GradeSteps() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.gradeSteps
}

// RegisterInsured 登记被保人。
func (e *Engine) RegisterInsured(id string, day int) error {
	const op = "RegisterInsured"
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || day < 0 {
		return newErr(op, ErrInvalidParam, "被保人编号为空或日为负")
	}
	if _, ok := e.insureds[id]; ok {
		return newErr(op, ErrInvalidParam, "被保人已登记: %q", id)
	}
	if err := e.checkClock(op, day); err != nil {
		return err
	}
	e.insureds[id] = newInsured()
	e.commitClock(day)
	return nil
}

// Level 返回被保人当前等级。
func (e *Engine) Level(id string) (int, error) {
	const op = "Level"
	e.mu.Lock()
	defer e.mu.Unlock()
	ins, err := e.mustInsured(op, id)
	if err != nil {
		return 0, err
	}
	if len(ins.years) == 0 {
		return 0, newErr(op, ErrInvalidParam, "被保人尚无保单: %q", id)
	}
	return ins.years[len(ins.years)-1].level, nil
}

// Years 返回被保人全部保单年度的只读快照（按办理先后排列）。
func (e *Engine) Years(id string) ([]YearInfo, error) {
	const op = "Years"
	e.mu.Lock()
	defer e.mu.Unlock()
	ins, err := e.mustInsured(op, id)
	if err != nil {
		return nil, err
	}
	out := make([]YearInfo, len(ins.years))
	for i, y := range ins.years {
		out[i] = YearInfo{Start: y.start, End: y.end, Level: y.level, Claims: len(y.claims), Protected: y.protected}
	}
	return out, nil
}

// Surcharges 返回被保人的保费差额追补记录（按产生先后排列）。
func (e *Engine) Surcharges(id string) []Surcharge {
	e.mu.Lock()
	defer e.mu.Unlock()
	ins, ok := e.insureds[id]
	if !ok {
		return nil
	}
	out := make([]Surcharge, len(ins.surcharges))
	copy(out, ins.surcharges)
	return out
}

// mustInsured 按拒绝次序校验被保人存在，调用时须持锁。
func (e *Engine) mustInsured(op, id string) (*insured, *Error) {
	ins, ok := e.insureds[id]
	if !ok {
		return nil, newErr(op, ErrInsuredNotFound, "被保人不存在: %q", id)
	}
	return ins, nil
}

// checkClock 校验时钟回退，调用时须持锁。
func (e *Engine) checkClock(op string, day int) *Error {
	if day < e.clock {
		return newErr(op, ErrClockRegression, "当前时刻 %d，入参日 %d", e.clock, day)
	}
	return nil
}

// commitClock 在操作成功的最后一步推进时钟，调用时须持锁。
func (e *Engine) commitClock(day int) {
	if day > e.clock {
		e.clock = day
	}
}
