// Package ncd 实现车险无赔款优惠（NCD）等级的续保定级引擎。
//
// 职责划分：
//   - engine.go：配置、时钟、并发入口与各操作的拒绝次序；
//   - insured.go：被保人状态、保单年度定位与追溯重定级；
//   - grading.go：单年度等级升降裁定的纯函数；
//   - errors.go：可区分的错误码，声明顺序即拒绝优先级。
package ncd

import (
	"fmt"
	"sync"
)

// Config 登记时给出的业务参数。
type Config struct {
	MaxLevel          int     // 最高等级（正整数），等级取值 0..MaxLevel
	RenewalGraceDays  int     // 续保窗口右端：到期日后 N 天（含），正整数
	LiableThreshold   int     // 有责门槛：责任比例不小于该值即有责，0..100
	ProtectStartLevel int     // 保护起始级：等级不低于它才可购买保护，0..MaxLevel
	Premiums          []int64 // 各等级保费表，长度须为 MaxLevel+1
}

func (c Config) validate() error {
	if c.MaxLevel < 1 {
		return fmt.Errorf("ncd: MaxLevel 须为正整数，got %d", c.MaxLevel)
	}
	if c.RenewalGraceDays < 1 {
		return fmt.Errorf("ncd: RenewalGraceDays 须为正整数，got %d", c.RenewalGraceDays)
	}
	if c.LiableThreshold < 0 || c.LiableThreshold > 100 {
		return fmt.Errorf("ncd: LiableThreshold 须在 [0,100]，got %d", c.LiableThreshold)
	}
	if c.ProtectStartLevel < 0 || c.ProtectStartLevel > c.MaxLevel {
		return fmt.Errorf("ncd: ProtectStartLevel 须在 [0,MaxLevel]，got %d", c.ProtectStartLevel)
	}
	if len(c.Premiums) != c.MaxLevel+1 {
		return fmt.Errorf("ncd: Premiums 长度须为 MaxLevel+1=%d，got %d", c.MaxLevel+1, len(c.Premiums))
	}
	return nil
}

// Engine 续保定级引擎。所有入口可并发调用，内部以互斥锁串行化，
// 结果等价于按锁获得顺序的某个串行执行。
type Engine struct {
	mu    sync.Mutex
	cfg   Config
	now   int // 当前时刻（整数天），只能前进
	ins   map[string]*insured
	steps int // 上一次操作访问的保单年度记录数（可验证的性能探针）
}

// NewEngine 按登记参数创建引擎；参数非法返回错误。
func NewEngine(cfg Config) (*Engine, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Engine{cfg: cfg, ins: make(map[string]*insured)}, nil
}

// Now 返回当前时刻。
func (e *Engine) Now() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// LastOpSteps 返回上一次操作访问的保单年度记录数，用于验证
// 续保定级 O(1) 与重定级范围。仅供测试与审计。
func (e *Engine) LastOpSteps() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.steps
}

// Insure 首次投保或中断后重新投保：新保单年度自办理日起算，等级为 0。
// 已有在保保单（办理日仍被某年度覆盖）时报「已有在保保单」。
func (e *Engine) Insure(insuredID, policyID string, day int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.steps = 0
	const op = "Insure"
	if insuredID == "" || policyID == "" || day < 0 {
		return fail(op, CodeInvalidParam, "被保人/保单编号为空或时刻为负")
	}
	if day < e.now {
		return fail(op, CodeClockRollback, fmt.Sprintf("办理日 %d 早于当前时刻 %d", day, e.now))
	}
	in, ok := e.ins[insuredID]
	if ok {
		last := in.years[len(in.years)-1]
		if day < last.start+policyYearDays {
			return fail(op, CodeActivePolicyExists, "须先办理转移")
		}
		in.closePolicy(day, "expired")
	} else {
		in = newInsured()
		e.ins[insuredID] = in
	}
	in.years = append(in.years, yearRec{start: day, base: true})
	in.policies = append(in.policies, PolicyRecord{PolicyID: policyID, FromDay: day, ToDay: -1})
	e.now = day
	return nil
}

// Renew 办理续保，返回新年度等级。
// 窗口 [到期日-30, 到期日+N] 内为连续续保，新年度自原到期日起算；
// 早于窗口左端报「续保窗口外」；晚于窗口右端视为中断，等级清零按首次投保处理。
func (e *Engine) Renew(insuredID string, day int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.steps = 0
	const op = "Renew"
	if insuredID == "" || day < 0 {
		return -1, fail(op, CodeInvalidParam, "被保人编号为空或时刻为负")
	}
	in, ok := e.ins[insuredID]
	if !ok {
		return -1, fail(op, CodeInsuredNotFound, insuredID)
	}
	if day < e.now {
		return -1, fail(op, CodeClockRollback, fmt.Sprintf("办理日 %d 早于当前时刻 %d", day, e.now))
	}
	last := &in.years[len(in.years)-1]
	expiry := last.start + policyYearDays
	if day < expiry-earlyRenewalDays {
		return -1, fail(op, CodeOutsideRenewalWindow,
			fmt.Sprintf("办理日 %d 早于窗口左端 %d", day, expiry-earlyRenewalDays))
	}
	var level int
	if day <= expiry+e.cfg.RenewalGraceDays {
		e.steps++ // 常规续保只读取上一保单年度的汇总，与历史长度无关
		level = gradeNext(last.level, last.liable, last.protected, e.cfg.MaxLevel)
		in.years = append(in.years, yearRec{start: expiry, level: level})
	} else {
		in.closePolicy(day, "expired")
		in.years = append(in.years, yearRec{start: day, base: true})
	}
	e.now = day
	return level, nil
}

// ReportClaim 登记出险。事故日早于当前年度生效日时归入历史年度并追溯重定级。
func (e *Engine) ReportClaim(insuredID, accidentID string, accidentDay, ratio, day int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.steps = 0
	const op = "ReportClaim"
	if insuredID == "" || accidentID == "" || day < 0 || accidentDay < 0 ||
		ratio < 0 || ratio > 100 || accidentDay > day {
		return fail(op, CodeInvalidParam, "编号为空、时刻为负、责任比例越界或事故日晚于当前时刻")
	}
	in, ok := e.ins[insuredID]
	if !ok {
		return fail(op, CodeInsuredNotFound, insuredID)
	}
	if day < e.now {
		return fail(op, CodeClockRollback, fmt.Sprintf("登记日 %d 早于当前时刻 %d", day, e.now))
	}
	if _, dup := in.claimYear[accidentID]; dup {
		return fail(op, CodeClaimExists, accidentID)
	}
	yi := in.findYear(accidentDay)
	if yi < 0 {
		return fail(op, CodeAccidentNotCovered, fmt.Sprintf("事故日 %d 未承保", accidentDay))
	}
	y := &in.years[yi]
	y.claims = append(y.claims, claimRec{id: accidentID, day: accidentDay, ratio: ratio})
	if ratio >= e.cfg.LiableThreshold {
		y.liable++
	}
	in.claimYear[accidentID] = yi
	e.recompute(in, yi)
	e.now = day
	return nil
}

// WithdrawClaim 撤销出险，触发追溯重定级，等级轨迹与该事故从未登记时一致。
func (e *Engine) WithdrawClaim(insuredID, accidentID string, day int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.steps = 0
	const op = "WithdrawClaim"
	if insuredID == "" || accidentID == "" || day < 0 {
		return fail(op, CodeInvalidParam, "编号为空或时刻为负")
	}
	in, ok := e.ins[insuredID]
	if !ok {
		return fail(op, CodeInsuredNotFound, insuredID)
	}
	if day < e.now {
		return fail(op, CodeClockRollback, fmt.Sprintf("撤销日 %d 早于当前时刻 %d", day, e.now))
	}
	yi, ok := in.claimYear[accidentID]
	if !ok {
		return fail(op, CodeClaimNotFound, accidentID)
	}
	y := &in.years[yi]
	for i, c := range y.claims {
		if c.id == accidentID {
			y.claims = append(y.claims[:i], y.claims[i+1:]...)
			if c.ratio >= e.cfg.LiableThreshold {
				y.liable--
			}
			break
		}
	}
	delete(in.claimYear, accidentID)
	e.recompute(in, yi)
	e.now = day
	return nil
}

// BuyProtection 在购买日所在保单年度内购买一次等级保护。
func (e *Engine) BuyProtection(insuredID string, day int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.steps = 0
	const op = "BuyProtection"
	if insuredID == "" || day < 0 {
		return fail(op, CodeInvalidParam, "被保人编号为空或时刻为负")
	}
	in, ok := e.ins[insuredID]
	if !ok {
		return fail(op, CodeInsuredNotFound, insuredID)
	}
	if day < e.now {
		return fail(op, CodeClockRollback, fmt.Sprintf("购买日 %d 早于当前时刻 %d", day, e.now))
	}
	yi := in.findYear(day)
	if yi < 0 {
		return fail(op, CodeInvalidParam, fmt.Sprintf("购买日 %d 不在任何保单年度内", day))
	}
	y := &in.years[yi]
	if y.level < e.cfg.ProtectStartLevel {
		return fail(op, CodeLevelInsufficient,
			fmt.Sprintf("等级 %d 低于保护起始级 %d", y.level, e.cfg.ProtectStartLevel))
	}
	if y.protected {
		return fail(op, CodeAlreadyProtected, "该年度已购买")
	}
	y.protected = true
	// 若已提前续保出未来年度，其等级可能因保护而升高（不退费，仅更新在案等级）。
	e.recompute(in, yi)
	e.now = day
	return nil
}

// Transfer 跨车转移：原保单终止，新保单继承等级与当前年度出险记录。
func (e *Engine) Transfer(insuredID, newPolicyID string, day int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.steps = 0
	const op = "Transfer"
	if insuredID == "" || newPolicyID == "" || day < 0 {
		return fail(op, CodeInvalidParam, "被保人/保单编号为空或时刻为负")
	}
	in, ok := e.ins[insuredID]
	if !ok {
		return fail(op, CodeInsuredNotFound, insuredID)
	}
	if day < e.now {
		return fail(op, CodeClockRollback, fmt.Sprintf("转移日 %d 早于当前时刻 %d", day, e.now))
	}
	in.closePolicy(day, "transfer")
	in.policies = append(in.policies, PolicyRecord{PolicyID: newPolicyID, FromDay: day, ToDay: -1})
	e.now = day
	return nil
}

// YearInfo 保单年度的只读快照，供查询与测试。
type YearInfo struct {
	Start     int
	Level     int
	Base      bool
	Protected bool
	Liable    int
	Claims    int
}

// Level 返回最新裁定等级（最后一个保单年度的等级）。
func (e *Engine) Level(insuredID string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ok := e.ins[insuredID]
	if !ok {
		return -1, fail("Level", CodeInsuredNotFound, insuredID)
	}
	return in.years[len(in.years)-1].level, nil
}

// Years 返回全部保单年度快照。
func (e *Engine) Years(insuredID string) ([]YearInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ok := e.ins[insuredID]
	if !ok {
		return nil, fail("Years", CodeInsuredNotFound, insuredID)
	}
	out := make([]YearInfo, len(in.years))
	for i, y := range in.years {
		out[i] = YearInfo{Start: y.start, Level: y.level, Base: y.base,
			Protected: y.protected, Liable: y.liable, Claims: len(y.claims)}
	}
	return out, nil
}

// Surcharges 返回保费差额追补记录（按产生顺序）。
func (e *Engine) Surcharges(insuredID string) ([]Surcharge, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ok := e.ins[insuredID]
	if !ok {
		return nil, fail("Surcharges", CodeInsuredNotFound, insuredID)
	}
	return append([]Surcharge(nil), in.surcharges...), nil
}

// Policies 返回保单审计记录。
func (e *Engine) Policies(insuredID string) ([]PolicyRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ok := e.ins[insuredID]
	if !ok {
		return nil, fail("Policies", CodeInsuredNotFound, insuredID)
	}
	return append([]PolicyRecord(nil), in.policies...), nil
}
