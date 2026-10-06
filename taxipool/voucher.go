package taxipool

import "time"

// Voucher 是一张短途返回优先凭证。
type Voucher struct {
	IssuedAt  time.Time
	ExpiresAt time.Time
	DayKey    string
	consumed  bool
	voided    bool
	// restrictedAttempts：因优先名额满而按普通入队的次数：第一次保留，第二次作废。
	restrictedAttempts int
}

// voucherLedger TODO
type voucherLedger struct {
	cfg     *Config
	pending map[string]*Voucher
	dayUsed map[string]map[string]int
}

// evaluateParams 汇总一次入池时凭证判定所需的输入。
type evaluateParams struct {
	driver string
	at     time.Time
	// 上一次完成行程的离开时刻（零值表示无）。
	leftAt time.Time
	// 该次行程距离。
	distance float64
}

// usableAt 判断凭证在时刻 at 是否可用：恰等于过期时刻视为已过期。
func (v *Voucher) usableAt(at time.Time) bool {
	return v != nil && !v.consumed && !v.voided && at.Before(v.ExpiresAt)
}

func newVoucherLedger(cfg *Config) *voucherLedger {
	return &voucherLedger{
		cfg:     cfg,
		pending: make(map[string]*Voucher),
		dayUsed: make(map[string]map[string]int),
	}
}

// evaluate 决定一次入池可使用的凭证（可能是新发放的，也可能是已有凭证）。
// 返回凭证与 issued 标志；issued=true 表示本次新发放（即便随后因满员/禁入被拒，
// 凭证也已发放并按发放时刻计时）。司机已有未消耗凭证时不会重复发放。
func (l *voucherLedger) evaluate(p evaluateParams) (v *Voucher, issued bool) {
	if ex, ok := l.pending[p.driver]; ok {
		if ex.usableAt(p.at) {
			return ex, false
		}
		delete(l.pending, p.driver)
	}
	if !p.leftAt.IsZero() &&
		p.at.Sub(p.leftAt) <= l.cfg.ReturnLimit &&
		p.distance <= l.cfg.ShortTripMeters {
		day := l.cfg.dayKey(p.at)
		used := l.dayUsed[p.driver]
		if used == nil {
			used = make(map[string]int)
			l.dayUsed[p.driver] = used
		}
		if used[day] < l.cfg.DailyVoucherLimit {
			v = &Voucher{
				IssuedAt:  p.at,
				ExpiresAt: p.at.Add(l.cfg.VoucherTTL),
				DayKey:    day,
			}
			used[day]++
			l.pending[p.driver] = v
			return v, true
		}
	}
	return nil, false
}

// consume 标记凭证已用于本次优先入池。
func (l *voucherLedger) consume(driver string) {
	if v, ok := l.pending[driver]; ok {
		v.consumed = true
		delete(l.pending, driver)
	}
}

// noteRestricted 记录一次因优先名额满而按普通入队：第一次保留凭证，第二次作废。
func (l *voucherLedger) noteRestricted(driver string) {
	v, ok := l.pending[driver]
	if !ok {
		return
	}
	v.restrictedAttempts++
	if v.restrictedAttempts >= 2 {
		v.voided = true
		delete(l.pending, driver)
	}
}

// pendingVoucher 返回司机当前持有的凭证（可能已过期，由调用方判断）。
func (l *voucherLedger) pendingVoucher(driver string) *Voucher { return l.pending[driver] }

// hasPending 返回司机是否持有未消耗/未作废的凭证（不判时效）。
func (l *voucherLedger) hasPending(driver string) bool {
	v, ok := l.pending[driver]
	return ok && !v.consumed && !v.voided
}

// dayUsedCount 返回司机在某自然日已获得的凭证数。
func (l *voucherLedger) dayUsedCount(driver string, day string) int {
	return l.dayUsed[driver][day]
}
