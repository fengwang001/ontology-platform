package apportion

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// 受理结论。
const (
	ConclusionPaid     = "赔付"
	ConclusionNoPolicy = "无可赔保单"
)

// Payout 是一张保单在一次损失中的应赔裁定。
type Payout struct {
	PolicyNo string
	Amount   int64
}

// Outcome 是一次受理的结果：结论、逐单应赔与受理后各参与保单的年度累计剩余。
type Outcome struct {
	LossNo     string
	Insured    string
	Conclusion string
	Payouts    []Payout
	Remaining  map[string]int64
}

type account struct {
	mu        sync.Mutex // 同一被保人串行化
	policies  []*Policy
	remaining map[string]int64 // 年度累计剩余
	order     []string         // 受理次序（损失号）
	entries   map[string]*entry
	knownLoss map[string]bool // 本账户在册损失号（身份判定在账户锁内完成）
}

type entry struct {
	loss     Loss
	payouts  []Payout
	decision *decision
}

// Engine 是分摊赔付引擎。不同被保人可并发，同一被保人等价于某种串行顺序。
type Engine struct {
	mu       sync.Mutex
	accounts map[string]*account
	policyNo map[string]string // 保单编号 -> 被保人（保单编号全局唯一）
	losses   map[string]string // 损失号 -> 被保人（损失号全局唯一）
	logw     io.Writer
}

// NewEngine 创建引擎；logw 非 nil 时打印每次操作的输入、输出与判定依据。
func NewEngine(logw io.Writer) *Engine {
	return &Engine{
		accounts: map[string]*account{},
		policyNo: map[string]string{},
		losses:   map[string]string{},
		logw:     logw,
	}
}

// getAccount 在全局锁内取（或惰性创建）被保人账户。
func (e *Engine) getAccount(insured string) *account {
	a := e.accounts[insured]
	if a == nil {
		a = &account{
			remaining: map[string]int64{},
			entries:   map[string]*entry{},
			knownLoss: map[string]bool{},
		}
		e.accounts[insured] = a
	}
	return a
}

// Register 登记一张保单。
func (e *Engine) Register(p Policy) error {
	if err := p.validate(); err != nil {
		return err
	}
	e.mu.Lock()
	if _, dup := e.policyNo[p.PolicyNo]; dup {
		e.mu.Unlock()
		e.logf("登记拒绝 保单=%s 依据=%s\n", p.PolicyNo, ErrPolicyDup)
		return ruleError(ErrPolicyDup)
	}
	a := e.getAccount(p.Insured)
	e.policyNo[p.PolicyNo] = p.Insured
	pc := p
	a.policies = append(a.policies, &pc)
	a.remaining[p.PolicyNo] = p.AnnualLimit
	e.mu.Unlock()
	e.logf("登记成功 被保人=%s 保单=%s 免赔=%d 每次限额=%d 年度限额=%d 区间=[%d,%d) 条款=%d\n",
		p.Insured, p.PolicyNo, p.Deductible, p.PerLoss, p.AnnualLimit, p.StartDay, p.EndDay, p.Clause)
	return nil
}

// Accept 受理一次损失并立即裁定、扣减年度累计限额。
func (e *Engine) Accept(l Loss) (*Outcome, error) {
	if err := l.validate(); err != nil {
		return nil, err
	}

	// 全局阶段仅判定被保人存在性；损失号唯一性在账户锁内判定，
	// 全局损失表只在全局锁下读改写，避免与撤销并发产生数据竞争。
	e.mu.Lock()
	a := e.accounts[l.Insured]
	if a == nil {
		e.mu.Unlock()
		e.logf("受理拒绝 损失=%s 依据=%s\n", l.LossNo, ErrInsuredMissing)
		return nil, ruleError(ErrInsuredMissing)
	}
	_, globalDup := e.losses[l.LossNo]
	e.mu.Unlock()

	// 账户阶段：同一被保人串行，扣减与撤销的可见性由此保证。
	a.mu.Lock()
	// 全局快照若已见该号必属重复；未见时再以本账户表为准。
	// 与撤销竞争的极端情况由全局锁二次校验兜住（账户锁内再取一次全局锁）。
	if globalDup || a.knownLoss[l.LossNo] {
		a.mu.Unlock()
		e.logf("受理拒绝 损失=%s 依据=%s\n", l.LossNo, ErrLossDup)
		return nil, ruleError(ErrLossDup)
	}
	out, ent, err := e.settleLocked(a, l)
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	// 提交前在持账户锁情况下再取全局锁做最终唯一性判定。
	// 此处锁顺序恒为「账户锁 -> 全局锁」；撤销恒为「全局锁 -> 账户锁」，
	// 为避免锁反转，撤销侧改为不在同一临界区嵌套（见 Undo）。
	e.mu.Lock()
	if _, dup := e.losses[l.LossNo]; dup {
		e.mu.Unlock()
		a.mu.Unlock()
		e.logf("受理拒绝 损失=%s 依据=%s\n", l.LossNo, ErrLossDup)
		return nil, ruleError(ErrLossDup)
	}
	a.order = append(a.order, l.LossNo)
	a.entries[l.LossNo] = ent
	a.knownLoss[l.LossNo] = true
	e.losses[l.LossNo] = l.Insured
	e.mu.Unlock()
	a.mu.Unlock()

	e.logDecision(l, out, ent.decision)
	return out, nil
}

// settleLocked 仅读取/扣减本账户状态，构建参与者快照并裁定。
func (e *Engine) settleLocked(a *account, l Loss) (*Outcome, *entry, error) {
	var parts []*participant
	for _, p := range a.policies {
		rem := a.remaining[p.PolicyNo]
		if rem <= 0 || !p.covers(l.Day) {
			continue // 年度累计恰好耗尽视同不存在；区间右端不含
		}
		parts = append(parts, &participant{
			policy: p, remaining: rem,
			il: independentLiability(p, rem, l.Amount),
		})
	}
	sortParticipants(parts)

	out := &Outcome{LossNo: l.LossNo, Insured: l.Insured, Remaining: map[string]int64{}}
	ent := &entry{loss: l}
	if len(parts) == 0 {
		out.Conclusion = ConclusionNoPolicy
		ent.decision = nil
	} else {
		out.Conclusion = ConclusionPaid
		d := settle(parts, l.Amount)
		ent.decision = d
		for _, st := range d.allStages() {
			for _, r := range st.policies {
				if r.pay == 0 {
					continue
				}
				a.remaining[r.policyNo] -= r.pay
				ent.payouts = append(ent.payouts, Payout{PolicyNo: r.policyNo, Amount: r.pay})
			}
		}
		sort.Slice(ent.payouts, func(i, j int) bool {
			return ent.payouts[i].PolicyNo < ent.payouts[j].PolicyNo
		})
		out.Payouts = append(out.Payouts, ent.payouts...)
	}
	for _, p := range a.policies {
		out.Remaining[p.PolicyNo] = a.remaining[p.PolicyNo]
	}
	return out, ent, nil
}

// Undo 撤销损失；仅允许撤销该被保人名下受理次序最后的一笔。
func (e *Engine) Undo(lossNo, insured string) (*Outcome, error) {
	if lossNo == "" || insured == "" {
		return nil, ruleError(ErrInvalid)
	}

	// 存在性/被保人判定先取全局锁快照；末笔判定与恢复在账户锁内完成。
	e.mu.Lock()
	a := e.accounts[insured]
	if a == nil {
		e.mu.Unlock()
		e.logf("撤销拒绝 损失=%s 依据=%s\n", lossNo, ErrInsuredMissing)
		return nil, ruleError(ErrInsuredMissing)
	}
	if _, exists := e.losses[lossNo]; !exists {
		e.mu.Unlock()
		e.logf("撤销拒绝 损失=%s 依据=%s\n", lossNo, ErrLossMissing)
		return nil, ruleError(ErrLossMissing)
	}
	e.mu.Unlock()

	// 锁层次统一为「账户锁 -> 全局锁」，与受理提交路径一致，杜绝锁反转。
	a.mu.Lock()
	if len(a.order) == 0 || a.order[len(a.order)-1] != lossNo {
		a.mu.Unlock()
		e.logf("撤销拒绝 损失=%s 依据=%s\n", lossNo, ErrNotLast)
		return nil, ruleError(ErrNotLast)
	}

	e.mu.Lock()
	owner, exists := e.losses[lossNo]
	if !exists || owner != insured {
		e.mu.Unlock()
		a.mu.Unlock()
		e.logf("撤销拒绝 损失=%s 依据=%s\n", lossNo, ErrLossMissing)
		return nil, ruleError(ErrLossMissing)
	}
	ent := a.entries[lossNo]
	for _, pt := range ent.payouts {
		a.remaining[pt.PolicyNo] += pt.Amount
	}
	a.order = a.order[:len(a.order)-1]
	delete(a.entries, lossNo)
	delete(a.knownLoss, lossNo)
	delete(e.losses, lossNo)
	e.mu.Unlock()

	out := &Outcome{LossNo: lossNo, Insured: insured, Conclusion: "撤销",
		Remaining: map[string]int64{}}
	for _, p := range a.policies {
		out.Remaining[p.PolicyNo] = a.remaining[p.PolicyNo]
	}
	a.mu.Unlock()

	e.logf("撤销成功 损失=%s 恢复应赔=%v 余额=%v\n", lossNo, ent.payouts, out.Remaining)
	return out, nil
}

// Remaining 返回某被保人各保单当前年度累计剩余（快照），便于测试与复算。
func (e *Engine) Remaining(insured string) map[string]int64 {
	e.mu.Lock()
	a := e.accounts[insured]
	e.mu.Unlock()
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := map[string]int64{}
	for k, v := range a.remaining {
		cp[k] = v
	}
	return cp
}

func (e *Engine) logf(format string, args ...any) {
	if e.logw != nil {
		fmt.Fprintf(e.logw, format, args...)
	}
}

func (e *Engine) logDecision(l Loss, out *Outcome, d *decision) {
	if e.logw == nil {
		return
	}
	fmt.Fprintf(e.logw, "受理 损失=%s 被保人=%s 损失日=%d 金额=%d 结论=%s 应赔=%v\n",
		l.LossNo, l.Insured, l.Day, l.Amount, out.Conclusion, out.Payouts)
	if d == nil {
		fmt.Fprintf(e.logw, "  依据: 无覆盖损失日且年度累计有余的保单\n")
		return
	}
	for _, st := range d.allStages() {
		fmt.Fprintf(e.logw, "  阶段=%s 目标=%d 实赔=%d\n", st.name, st.target, st.paid)
		for _, r := range st.policies {
			fmt.Fprintf(e.logw, "    保单=%s IL=%d 权重=%d 应赔=%d\n",
				r.policyNo, r.il, r.weight, r.pay)
		}
	}
	fmt.Fprintf(e.logw, "  受理后年度累计剩余=%v\n", out.Remaining)
}
