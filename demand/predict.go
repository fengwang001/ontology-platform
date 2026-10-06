package demand

import "math/big"

// forecast 描述一个活跃窗口的预测依据，用于日志与结果展示。
type forecast struct {
	end      int64
	occurred *frac
	future   *frac
	limit    *frac
	exceeds  bool
}

// winTerm 为单个活跃窗口预算好的判定常数，使切除/恢复的可行判定
// 只需整数算术，不再重复对切片做大数求和。
//
// 越限条件：occ + (P - x)*(end-now) > limit
// 等价于：x*remain < occ + P*remain - limit（记为 need > 0），
// 恢复时 x 为负（净恢复容量）。
type winTerm struct {
	end    int64
	remain int64
	// slack = limit - occ（千瓦秒），以 slackNum/den 精确表示。
	slackNum int64
	den      int64
}

type predictor struct {
	ring *windowRing
	cfg  Config

	now   int64
	lastP *frac // 最近一次被接受上报区间功率（千瓦）
	pNum  int64 // 最近区间功率分子（区间用电量，千瓦秒）
	pDen  int64 // 最近区间功率分母（区间长度，秒）
	terms []winTerm
}

func newPredictor(cfg Config, ring *windowRing) *predictor {
	return &predictor{ring: ring, cfg: cfg, lastP: newFrac()}
}

// prepare 在 now 时刻预算所有活跃窗口的常数。
func (p *predictor) prepare(now int64) {
	p.now = now
	p.terms = p.terms[:0]
	limit := fracInt(p.cfg.ContractKW).mulInt(p.cfg.WindowSec)
	for _, end := range p.ring.activeEnds(now) {
		slack := limit.clone().sub(p.ring.windowEnergy(end))
		p.terms = append(p.terms, winTerm{
			end:      end,
			remain:   end - now,
			slackNum: slack.num(),
			den:      slack.den(),
		})
	}
}

// feasible 判定净切除 x（恢复为负）后所有窗口均不越限。
func (p *predictor) feasible(x int64) bool {
	for _, tm := range p.terms {
		if !p.termFeasible(tm, x) {
			return false
		}
	}
	return true
}

// termFeasible 用整数交叉相乘判定：
// 有效功率 eff = P-x（不低于0），要求 eff*remain <= slack。
// P=pNum/pDen，slack=sn/sd，故
// (pNum - x*pDen)*remain*sd <= sn*pDen。
func (p *predictor) termFeasible(tm winTerm, x int64) bool {
	effNum := p.pNum - x*p.pDen
	if effNum < 0 {
		return tm.slackNum >= 0
	}
	sn, sd := tm.slackNum, tm.den
	var lhs, rhs big.Int
	lhs.Mul(big.NewInt(effNum), big.NewInt(tm.remain)).Mul(&lhs, big.NewInt(sd))
	rhs.Mul(big.NewInt(sn), big.NewInt(p.pDen))
	return lhs.Cmp(&rhs) <= 0
}

// evaluate 返回展示用预测明细与是否越限（x 为净切除）。
func (p *predictor) evaluate(now, x int64) ([]forecast, bool) {
	effP := p.lastP.clone().sub(fracInt(x))
	if effP.cmp(zero) < 0 {
		effP.set(zero)
	}
	any := false
	out := make([]forecast, 0, len(p.terms))
	for _, tm := range p.terms {
		fut := effP.clone().mulInt(tm.remain)
		slack := newFrac()
		slack.v.SetFrac(big.NewInt(tm.slackNum), big.NewInt(tm.den))
		ex := fut.cmp(slack) > 0
		any = any || ex
		out = append(out, forecast{
			end: tm.end, occurred: fracInt(0), future: fut,
			limit: slack, exceeds: ex,
		})
	}
	return out, any
}

// requiredShed 返回使所有活跃窗口不越限所需的最小切除容量（精确值）。
// 对每个窗口：x >= P - (limit-occ)/remain；取最大值。
func (p *predictor) requiredShed() *frac {
	req := newFrac()
	for _, tm := range p.terms {
		var need *frac
		if tm.remain <= 0 {
			if tm.slackNum < 0 {
				need = p.lastP.clone().add(fracInt(1))
			} else {
				continue
			}
		} else {
			allowed := fracOver(tm.slackNum, tm.den*tm.remain)
			need = p.lastP.clone().sub(allowed)
		}
		if need.cmp(req) > 0 {
			req = need
		}
	}
	if req.cmp(zero) < 0 {
		req.set(zero)
	}
	return req
}
