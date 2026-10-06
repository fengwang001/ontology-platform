package policy

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveModel 是按规格独立写成的朴素参考模型。
// 它不维护任何累计量/堆：每次计算都从完整事件列表重放，
// 每次找应生效批改都全量扫描所有批改，故意做成 O(历史长度)。
type naiveModel struct {
	cfg  Config
	now  int64
	loan int64
	dead bool
	ends map[string]*naiveEnd
	evs  []naiveEvent // 全部已落地事件，按全局次序追加
}

type naiveEnd struct {
	id, ben   string
	kind      Kind
	apply     int64
	eff       int64
	seq       int64
	status    Status
	newSA     int64
	newPay    int64
	surcharge int64
	refund    int64
}

type naiveEvent struct {
	order  int64
	at     int64
	kind   int // 1 缴费 2 保额变更生效 3 部分退保
	year   int64
	amt    int64 // 缴费/补缴金额；事件2为补缴金额
	sa     int64 // 事件2的新保额
	refund int64 // 事件2/3的退还金额
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg, ends: map[string]*naiveEnd{}}
}

func (m *naiveModel) yearAt(day int64) int64 { return (day-m.cfg.EffectiveDay)/365 + 1 }

func (m *naiveModel) ratio(year int64) int64 {
	t := m.cfg.RatioTable
	i := int(year - 1)
	if i >= len(t) {
		i = len(t) - 1
	}
	return t[i]
}

// replay 全量重放至 day（含），返回保额、年缴保费、各年缴费、累计实缴。
func (m *naiveModel) replay(day int64) (sa, premium int64, paidYears map[int64]int64, paidTotal int64) {
	sa = m.cfg.BaseSumAssured
	premium = m.cfg.AnnualPremium
	paidYears = map[int64]int64{}
	evs := make([]naiveEvent, len(m.evs))
	copy(evs, m.evs)
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].order < evs[j].order })
	for _, e := range evs {
		if e.at > day {
			continue
		}
		switch e.kind {
		case 1:
			if _, ok := paidYears[e.year]; !ok {
				paidYears[e.year] = e.amt
				paidTotal += e.amt
			}
		case 2:
			oldSA := sa
			sa = e.sa
			premium = (premium*sa + oldSA - 1) / oldSA
			paidTotal += e.amt - e.refund
		case 3:
			oldSA := sa
			sa = e.sa
			premium = (premium*sa + oldSA - 1) / oldSA
			paidTotal = paidTotal * sa / oldSA
		}
	}
	return
}

func (m *naiveModel) cashValue(day int64) int64 {
	_, _, _, paid := m.replay(day)
	gross := paid * m.ratio(m.yearAt(day)) / 100
	v := gross - m.loan
	if v < 0 {
		return 0
	}
	return v
}

func (m *naiveModel) inCooling(day int64) bool {
	return day-m.cfg.EffectiveDay < m.cfg.CoolingDays
}

var norder int64

func (m *naiveModel) addEvent(e naiveEvent) {
	norder++
	e.order = norder
	m.evs = append(m.evs, e)
}

func naiveCeilDiv(a, b int64) int64 { return (a + b - 1) / b }

// advance 推进时钟并全量扫描批改，按 (生效日, 申请次序) 依次生效。
func (m *naiveModel) advance(to int64) []string {
	if to < m.now {
		return nil
	}
	m.now = to
	var changed []string
	for {
		var cand *naiveEnd
		for _, en := range m.ends {
			if en.status != StatusScheduled || en.eff > m.now {
				continue
			}
			if cand == nil || en.eff < cand.eff || (en.eff == cand.eff && en.seq < cand.seq) {
				cand = en
			}
		}
		if cand == nil {
			return changed
		}
		m.applyEnd(cand)
		changed = append(changed, cand.id)
	}
}

func (m *naiveModel) schedule(req EndRequest, seq int64) error {
	if !req.valid() {
		return ErrInvalid
	}
	if m.dead {
		return ErrTerminated
	}
	if _, ok := m.ends[req.EndID]; ok {
		return ErrEndDuplicate
	}
	if req.EffectiveDay < req.ApplyDay || req.EffectiveDay < m.now ||
		req.EffectiveDay < m.lastEffDay() {
		return ErrRetroactive
	}
	en := &naiveEnd{
		id: req.EndID, kind: req.Kind, apply: req.ApplyDay, eff: req.EffectiveDay,
		seq: seq, status: StatusScheduled, newSA: req.NewSumAssured,
		newPay: req.NewPayPeriods, ben: req.NewBeneficiary,
	}
	m.ends[en.id] = en
	if en.eff <= m.now {
		m.applyEnd(en)
	}
	return nil
}

func (m *naiveModel) lastEffDay() int64 {
	last := int64(-1)
	for _, en := range m.ends {
		if en.status == StatusEffective && en.eff > last {
			last = en.eff
		}
	}
	return last
}

func (m *naiveModel) applyEnd(en *naiveEnd) {
	en.surcharge, en.refund = m.premiumDelta(en)
	if en.surcharge > 0 {
		en.status = StatusAwaitingPay
		return
	}
	m.finish(en, en.eff)
}

func (m *naiveModel) premiumDelta(en *naiveEnd) (surcharge, refund int64) {
	sa, premium, paid, _ := m.replay(en.eff)
	if en.kind != KindAmountChange || en.newSA == sa {
		return 0, 0
	}
	year := m.yearAt(en.eff)
	if _, ok := paid[year]; !ok {
		return 0, 0
	}
	yearStart := m.cfg.EffectiveDay + (year-1)*365
	remaining := yearStart + 365 - en.eff
	newPremium := naiveCeilDiv(premium*en.newSA, sa)
	diff := newPremium - premium
	if diff > 0 {
		return naiveCeilDiv(diff*remaining, 365), 0
	}
	if diff < 0 {
		return 0, (-diff) * remaining / 365
	}
	return 0, 0
}

func (m *naiveModel) finish(en *naiveEnd, at int64) {
	if en.kind == KindAmountChange {
		m.addEvent(naiveEvent{at: at, kind: 2, amt: en.surcharge, sa: en.newSA, refund: en.refund})
	}
	en.status = StatusEffective
}

func (m *naiveModel) cancel(id string) error {
	en, ok := m.ends[id]
	if !ok {
		return ErrEndMissing
	}
	if en.status == StatusEffective || en.status == StatusCancelled {
		return ErrEndEffective
	}
	en.status = StatusCancelled
	return nil
}

func (m *naiveModel) paySurcharge(id string, amt int64) error {
	en, ok := m.ends[id]
	if !ok {
		return ErrEndMissing
	}
	if en.status != StatusAwaitingPay {
		if en.status == StatusEffective {
			return ErrEndEffective
		}
		return ErrInvalid
	}
	if amt <= 0 || amt != en.surcharge {
		return ErrInvalid
	}
	m.finish(en, m.now)
	return nil
}

func (m *naiveModel) payPremium(year int64) error {
	if year <= 0 {
		return ErrInvalid
	}
	if m.dead {
		return ErrTerminated
	}
	cur := m.yearAt(m.now)
	if year != cur && year != cur+1 {
		return ErrInvalid
	}
	_, _, paid, _ := m.replay(m.now)
	if _, ok := paid[year]; ok {
		return ErrAlreadyPaid
	}
	_, premium, _, _ := m.replay(m.now)
	m.addEvent(naiveEvent{at: m.now, kind: 1, year: year, amt: premium})
	return nil
}

func (m *naiveModel) pendingBlock() error {
	hasScheduled := false
	for _, en := range m.ends {
		if en.status == StatusAwaitingPay {
			return ErrAwaitingPay
		}
		if en.status == StatusScheduled {
			hasScheduled = true
		}
	}
	if hasScheduled {
		return ErrPendingEnds
	}
	return nil
}

func (m *naiveModel) fullSurrender() (SurrenderResult, error) {
	if m.dead {
		return SurrenderResult{}, ErrTerminated
	}
	if err := m.pendingBlock(); err != nil {
		return SurrenderResult{}, err
	}
	sa, premium, _, _ := m.replay(m.now)
	var payout int64
	if m.inCooling(m.now) {
		_, _, _, paid := m.replay(m.now)
		payout = paid - m.cfg.PolicyFee
		if payout < 0 {
			payout = 0
		}
	} else {
		payout = m.cashValue(m.now)
	}
	m.dead = true
	return SurrenderResult{Payout: payout, CashValue: payout, AnnualPremium: premium, SumAssured: sa}, nil
}

func (m *naiveModel) partial(reduce int64) (SurrenderResult, error) {
	if reduce <= 0 {
		return SurrenderResult{}, ErrInvalid
	}
	if m.dead {
		return SurrenderResult{}, ErrTerminated
	}
	if m.inCooling(m.now) {
		return SurrenderResult{}, ErrInvalid
	}
	sa, premium, _, _ := m.replay(m.now)
	newSA := sa - reduce
	if newSA < m.cfg.MinSumAssured {
		return SurrenderResult{}, ErrBelowMinSA
	}
	cv := m.cashValue(m.now)
	payout := cv * reduce / sa
	m.addEvent(naiveEvent{at: m.now, kind: 3, sa: newSA})
	newPremium := naiveCeilDiv(premium*newSA, sa)
	return SurrenderResult{Payout: payout, CashValue: cv, AnnualPremium: newPremium, SumAssured: newSA}, nil
}

// TestNaiveDifferential 用随机操作序列对照朴素参考模型，
// 日志打印每步输入、双方输出与判定依据；一旦不一致即失败并转储全量日志。
func TestNaiveDifferential(t *testing.T) {
	if !testing.Verbose() {
		t.Log("(使用 -v 查看逐步输入/输出/判定依据日志)")
	}
	for seed := int64(1); seed <= 300; seed++ {
		runDiff(t, seed)
	}
}

type diffLog struct{ strings.Builder }

func runDiff(t *testing.T, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var log diffLog
	cfg := Config{
		EffectiveDay:   int64(rng.Intn(5)),
		AnnualPremium:  int64(100 + rng.Intn(2000)),
		BaseSumAssured: int64(5000 + rng.Intn(20000)),
		MinSumAssured:  int64(100 + rng.Intn(1000)),
		CoolingDays:    int64(rng.Intn(15)),
		PolicyFee:      int64(rng.Intn(100)),
		PayPeriods:     10,
		RatioTable:     []int64{int64(rng.Intn(120)), int64(rng.Intn(150)), int64(rng.Intn(200))},
	}
	if cfg.MinSumAssured > cfg.BaseSumAssured {
		cfg.MinSumAssured = cfg.BaseSumAssured
	}
	eng := New()
	nm := newNaive(cfg)
	engErr := eng.Register("p", cfg)
	naiveRegErr := error(nil)
	if !cfg.valid() {
		naiveRegErr = ErrInvalid
	}
	fmt.Fprintf(&log.Builder, "seed=%d cfg=%+v register(engine=%v naive=%v)\n",
		seed, cfg, engErr, naiveRegErr)
	if diffErr(t, engErr, naiveRegErr, &log, "register") {
		return
	}

	endCounter := 0
	var awaitingIDs []string
	var scheduledIDs []string
	now := int64(0)
	matchErr := func(step string, got, want error) {
		t.Helper()
		if !sameCode(got, want) {
			t.Fatalf("seed=%d step=%s error mismatch engine=%v naive=%v\n%s",
				seed, step, got, want, log.String())
		}
	}

	for step := 0; step < 120; step++ {
		op := rng.Intn(100)
		deadView, _ := eng.GetPolicy("p")
		dead := deadView.Terminated
		switch {
		case op < 25: // 推进时钟
			to := now + int64(rng.Intn(80))
			fmt.Fprintf(&log.Builder, "[%d] advance %d->%d\n", step, now, to)
			res, e1 := eng.AdvanceTime(to)
			ids := nm.advance(to)
			if now <= to {
				now = to
			}
			matchErr("advance", e1, nil)
			if e1 == nil && len(res) != len(ids) {
				t.Fatalf("seed=%d advance effective count %d vs %d\n%s",
					seed, len(res), len(ids), log.String())
			}
			for i, id := range ids {
				if i < len(res) && res[i].EndID != id {
					t.Fatalf("seed=%d order mismatch %s!=%s\n%s",
						seed, res[i].EndID, id, log.String())
				}
			}
			awaitingIDs, scheduledIDs = refreshEndLists(eng)
			fmt.Fprintf(&log.Builder, "    => applied=%v\n", ids)
		case op < 45: // 预约批改
			endCounter++
			id := fmt.Sprintf("e%d", endCounter)
			kindPick := rng.Intn(3)
			apply := now
			eff := now + int64(rng.Intn(120))
			if rng.Intn(5) == 0 {
				eff = now // 立即生效
			}
			req := EndRequest{PolicyID: "p", EndID: id, ApplyDay: apply, EffectiveDay: eff}
			switch kindPick {
			case 0:
				req.Kind = KindAmountChange
				req.NewSumAssured = int64(100 + rng.Intn(int(cfg.BaseSumAssured*2)))
			case 1:
				req.Kind = KindPayPeriodChange
				req.NewPayPeriods = int64(1 + rng.Intn(20))
			default:
				req.Kind = KindBeneficiaryChange
				req.NewBeneficiary = fmt.Sprintf("ben%d", rng.Intn(5))
			}
			e1 := eng.Schedule(req)
			n1 := nm.schedule(req, int64(endCounter))
			fmt.Fprintf(&log.Builder, "[%d] schedule %+v => engine=%v naive=%v\n", step, req, e1, n1)
			matchErr("schedule", e1, n1)
			awaitingIDs, scheduledIDs = refreshEndLists(eng)
		case op < 52 && len(scheduledIDs) > 0: // 撤销预约
			id := scheduledIDs[rng.Intn(len(scheduledIDs))]
			e1 := eng.Cancel("p", id)
			n1 := nm.cancel(id)
			fmt.Fprintf(&log.Builder, "[%d] cancel %s => %v/%v\n", step, id, e1, n1)
			matchErr("cancel", e1, n1)
			awaitingIDs, scheduledIDs = refreshEndLists(eng)
		case op < 60 && len(awaitingIDs) > 0: // 补缴
			id := awaitingIDs[rng.Intn(len(awaitingIDs))]
			info, _ := eng.GetEndorsement("p", id)
			amt := info.Surcharge
			if rng.Intn(4) == 0 {
				amt += 1 // 错额应被双方拒绝
			}
			_, e1 := eng.PaySurcharge("p", id, amt)
			n1 := nm.paySurcharge(id, amt)
			fmt.Fprintf(&log.Builder, "[%d] paySurcharge %s amt=%d => %v/%v\n", step, id, amt, e1, n1)
			matchErr("paySurcharge", e1, n1)
			awaitingIDs, scheduledIDs = refreshEndLists(eng)
		case op < 80: // 缴费
			curYear := (now-cfg.EffectiveDay)/365 + 1
			if now < cfg.EffectiveDay {
				curYear = 1
			}
			year := curYear
			if rng.Intn(2) == 0 {
				year = curYear + 1
			}
			if rng.Intn(8) == 0 {
				year += 5 // 非法年度
			}
			e1 := eng.PayPremium("p", year)
			n1 := nm.payPremium(year)
			fmt.Fprintf(&log.Builder, "[%d] pay year=%d => %v/%v\n", step, year, e1, n1)
			matchErr("pay", e1, n1)
		case op < 88: // 设借款并查现金价值
			loan := int64(rng.Intn(4000))
			e1 := eng.SetLoan("p", loan)
			if e1 == nil {
				nm.loan = loan
			}
			day := now
			cv1, _ := eng.CashValue("p", day, nm.loan)
			cv2 := nm.cashValue(day)
			fmt.Fprintf(&log.Builder, "[%d] loan=%d cv(day=%d)=%d/%d\n", step, nm.loan, day, cv1, cv2)
			if e1 == nil && cv1 != cv2 {
				t.Fatalf("seed=%d cv mismatch %d!=%d\n%s", seed, cv1, cv2, log.String())
			}
		case op < 95: // 部分退保
			v, _ := eng.GetPolicy("p")
			reduce := int64(1 + rng.Intn(int(v.SumAssured)))
			r1, e1 := eng.PartialSurrender("p", reduce)
			r2, n1 := nm.partial(reduce)
			fmt.Fprintf(&log.Builder, "[%d] partial reduce=%d => eng(%+v,%v) naive(%+v,%v)\n",
				step, reduce, r1, e1, r2, n1)
			matchErr("partial", e1, n1)
			if e1 == nil && (r1.Payout != r2.Payout || r1.SumAssured != r2.SumAssured ||
				r1.AnnualPremium != r2.AnnualPremium) {
				t.Fatalf("seed=%d partial result mismatch\n%s", seed, log.String())
			}
		default: // 整单退保
			r1, e1 := eng.FullSurrender("p")
			r2, n1 := nm.fullSurrender()
			fmt.Fprintf(&log.Builder, "[%d] fullSurrender => eng(%+v,%v) naive(%+v,%v)\n",
				step, r1, e1, r2, n1)
			matchErr("full", e1, n1)
			if e1 == nil && r1.Payout != r2.Payout {
				t.Fatalf("seed=%d payout mismatch %d!=%d\n%s",
					seed, r1.Payout, r2.Payout, log.String())
			}
		}
		if dead {
			break
		}
	}

	// 终态对齐：最终账目逐字段比较。
	ev, _ := eng.GetPolicy("p")
	sa, prem, _, paid := nm.replay(nm.now)
	fmt.Fprintf(&log.Builder, "FINAL engine(sa=%d prem=%d paid=%d dead=%v) naive(sa=%d prem=%d paid=%d dead=%v)\n",
		ev.SumAssured, ev.AnnualPremium, ev.PaidTotal, ev.Terminated, sa, prem, paid, nm.dead)
	if testing.Verbose() {
		t.Log(log.String())
	}
	if ev.SumAssured != sa || ev.AnnualPremium != prem || ev.PaidTotal != paid ||
		ev.Terminated != nm.dead {
		t.Fatalf("seed=%d final state mismatch\n%s", seed, log.String())
	}
}

func refreshEndLists(e *Engine) (awaiting, scheduled []string) {
	// 通过保单内部堆不可外部枚举，这里用 GetPolicy 不暴露批改列表；
	// 测试直接读取内部 map（同包测试）。
	p := e.policies["p"]
	for id, en := range p.endByID {
		switch en.status {
		case StatusAwaitingPay:
			awaiting = append(awaiting, id)
		case StatusScheduled:
			scheduled = append(scheduled, id)
		}
	}
	return
}

func sameCode(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}

func diffErr(t *testing.T, a, b error, log *diffLog, step string) bool {
	t.Helper()
	if !sameCode(a, b) {
		t.Fatalf("step=%s register mismatch %v vs %v\n%s", step, a, b, log.String())
		return true
	}
	return a != nil
}
