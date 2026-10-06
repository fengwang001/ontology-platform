package policy

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"
)

// 朴素模型：按规格独立写成的对照实现。用切片线性扫描与全量求和，
// 与引擎的“堆 + 滚动累计”实现相互印证，语义必须完全一致。

type naiveEndorsement struct {
	id                    string
	typ                   EndorsementType
	applyDay, effDay, seq int64
	state                 EndorsementState
	newSumInsured         int64
	newPremium            int64
	topUp                 int64
	newTerm               int
	beneficiary           string
}

type naivePolicy struct {
	effDate, premium, sumInsured, minSumInsured int64
	hesitation, issueFee                        int64
	ratios                                      []int64
	paymentTerm                                 int
	beneficiary                                 string
	now                                         int64
	terminated                                  bool
	payments                                    []int64
	adjustments                                 []int64
	paidYears                                   []int64
	endorsements                                []*naiveEndorsement
	seqCounter                                  int64
}

type naiveEngine struct {
	policies map[string]*naivePolicy
}

func newNaiveEngine() *naiveEngine {
	return &naiveEngine{policies: make(map[string]*naivePolicy)}
}

func (n *naiveEngine) register(in PolicyInput) {
	ratios := make([]int64, len(in.CashValueRatios))
	copy(ratios, in.CashValueRatios)
	n.policies[in.ID] = &naivePolicy{
		effDate:       in.EffectiveDate,
		premium:       in.AnnualPremium,
		sumInsured:    in.SumInsured,
		minSumInsured: in.MinSumInsured,
		hesitation:    in.HesitationDays,
		issueFee:      in.IssueFee,
		ratios:        ratios,
		paymentTerm:   in.PaymentTerm,
		beneficiary:   in.Beneficiary,
		now:           in.EffectiveDate,
	}
}

func (n *naivePolicy) totalPaid() int64 {
	sum := int64(0)
	for _, a := range n.payments {
		sum += a
	}
	for _, a := range n.adjustments {
		sum += a
	}
	return sum
}

func (n *naivePolicy) isPaid(year int64) bool {
	for _, y := range n.paidYears {
		if y == year {
			return true
		}
	}
	return false
}

func (n *naivePolicy) findEndorsement(id string) *naiveEndorsement {
	for _, en := range n.endorsements {
		if en.id == id {
			return en
		}
	}
	return nil
}

func (n *naivePolicy) maxEffDay() int64 {
	maxDay := int64(-1)
	for _, en := range n.endorsements {
		if en.state == StateEffective && en.effDay > maxDay {
			maxDay = en.effDay
		}
	}
	return maxDay
}

func (n *naivePolicy) hasPending() bool {
	for _, en := range n.endorsements {
		if en.state == StateScheduled || en.state == StatePendingTopUp {
			return true
		}
	}
	return false
}

func (n *naivePolicy) cashValue(day, loan int64) int64 {
	year := (day - n.effDate) / 365
	if year >= int64(len(n.ratios)) {
		year = int64(len(n.ratios)) - 1
	}
	cv := n.totalPaid()*n.ratios[year]/100 - loan
	if cv < 0 {
		return 0
	}
	return cv
}

func (n *naiveEngine) payPremium(id string, year, amount int64) error {
	if year < 0 || amount <= 0 {
		return newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := n.policies[id]
	if !ok {
		return newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return newErr(ErrTerminated, "已终态")
	}
	cur := (p.now - p.effDate) / 365
	if (year != cur && year != cur+1) || amount != p.premium {
		return newErr(ErrInvalidParam, "参数非法")
	}
	if p.isPaid(year) {
		return newErr(ErrAlreadyPaid, "已缴")
	}
	p.paidYears = append(p.paidYears, year)
	p.payments = append(p.payments, amount)
	return nil
}

func (n *naiveEngine) schedule(policyID string, in EndorsementInput) error {
	if in.ID == "" || in.ApplyDay < 0 || in.EffDay < 0 {
		return newErr(ErrInvalidParam, "参数非法")
	}
	switch in.Type {
	case EndorsementSumInsured:
		if in.NewSumInsured <= 0 {
			return newErr(ErrInvalidParam, "参数非法")
		}
	case EndorsementPaymentTerm:
		if in.NewTerm <= 0 {
			return newErr(ErrInvalidParam, "参数非法")
		}
	case EndorsementBeneficiary:
	default:
		return newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := n.policies[policyID]
	if !ok {
		return newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return newErr(ErrTerminated, "已终态")
	}
	if p.findEndorsement(in.ID) != nil {
		return newErr(ErrEndorsementDuplicate, "批改重复")
	}
	if in.EffDay < in.ApplyDay || in.EffDay < p.now || in.EffDay < p.maxEffDay() {
		return newErr(ErrRetroactive, "追溯批改")
	}
	p.endorsements = append(p.endorsements, &naiveEndorsement{
		id:            in.ID,
		typ:           in.Type,
		applyDay:      in.ApplyDay,
		effDay:        in.EffDay,
		seq:           p.seqCounter,
		state:         StateScheduled,
		newSumInsured: in.NewSumInsured,
		newTerm:       in.NewTerm,
		beneficiary:   in.Beneficiary,
	})
	p.seqCounter++
	return nil
}

func (n *naiveEngine) cancel(policyID, endorsementID string) error {
	p, ok := n.policies[policyID]
	if !ok {
		return newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return newErr(ErrTerminated, "已终态")
	}
	en := p.findEndorsement(endorsementID)
	if en == nil {
		return newErr(ErrEndorsementNotFound, "批改不存在")
	}
	switch en.state {
	case StateEffective:
		return newErr(ErrAlreadyEffective, "已生效")
	case StatePendingTopUp:
		return newErr(ErrPendingTopUp, "待补缴")
	}
	kept := p.endorsements[:0]
	for _, x := range p.endorsements {
		if x != en {
			kept = append(kept, x)
		}
	}
	p.endorsements = kept
	return nil
}

func (n *naiveEngine) payTopUp(policyID, endorsementID string) (int64, error) {
	p, ok := n.policies[policyID]
	if !ok {
		return 0, newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return 0, newErr(ErrTerminated, "已终态")
	}
	en := p.findEndorsement(endorsementID)
	if en == nil {
		return 0, newErr(ErrEndorsementNotFound, "批改不存在")
	}
	switch en.state {
	case StateEffective:
		return 0, newErr(ErrAlreadyEffective, "已生效")
	case StateScheduled:
		return 0, newErr(ErrInvalidParam, "参数非法: 补缴尚未产生")
	}
	amount := en.topUp
	p.adjustments = append(p.adjustments, amount)
	p.sumInsured = en.newSumInsured
	p.premium = en.newPremium
	en.state = StateEffective
	return amount, nil
}

func (n *naiveEngine) advance(id string, day int64) ([]Event, error) {
	if day < 0 {
		return nil, newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := n.policies[id]
	if !ok {
		return nil, newErr(ErrPolicyNotFound, "保单不存在")
	}
	if day < p.now {
		return nil, newErr(ErrClockRegression, "时钟回退")
	}
	if p.terminated {
		return nil, newErr(ErrTerminated, "已终态")
	}
	var due []*naiveEndorsement
	for _, en := range p.endorsements {
		if en.state == StateScheduled && en.effDay <= day {
			due = append(due, en)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].effDay != due[j].effDay {
			return due[i].effDay < due[j].effDay
		}
		return due[i].seq < due[j].seq
	})
	var events []Event
	for _, en := range due {
		events = append(events, p.effectuate(en))
	}
	p.now = day
	return events, nil
}

func (n *naivePolicy) effectuate(en *naiveEndorsement) Event {
	switch en.typ {
	case EndorsementSumInsured:
		newPremium := (n.premium*en.newSumInsured + n.sumInsured - 1) / n.sumInsured
		delta := newPremium - n.premium
		year := (en.effDay - n.effDate) / 365
		remaining := n.effDate + (year+1)*365 - en.effDay
		if n.isPaid(year) && delta > 0 {
			en.newPremium = newPremium
			en.topUp = (delta*remaining + 365 - 1) / 365
			en.state = StatePendingTopUp
			return Event{EndorsementID: en.id, Kind: EventPendingTopUp, TopUp: en.topUp}
		}
		var refund int64
		if n.isPaid(year) && delta < 0 {
			refund = (-delta) * remaining / 365
			n.adjustments = append(n.adjustments, -refund)
		}
		n.sumInsured = en.newSumInsured
		n.premium = newPremium
		en.state = StateEffective
		return Event{EndorsementID: en.id, Kind: EventEffective, Refund: refund}
	case EndorsementPaymentTerm:
		n.paymentTerm = en.newTerm
		en.state = StateEffective
		return Event{EndorsementID: en.id, Kind: EventEffective}
	case EndorsementBeneficiary:
		n.beneficiary = en.beneficiary
		en.state = StateEffective
		return Event{EndorsementID: en.id, Kind: EventEffective}
	}
	return Event{}
}

func (n *naiveEngine) cashValueQuery(id string, day, loan int64) (int64, error) {
	if day < 0 || loan < 0 {
		return 0, newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := n.policies[id]
	if !ok {
		return 0, newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return 0, newErr(ErrTerminated, "已终态")
	}
	if day < p.effDate {
		return 0, newErr(ErrInvalidParam, "参数非法")
	}
	return p.cashValue(day, loan), nil
}

func (n *naiveEngine) surrender(id string, loan int64) (int64, error) {
	if loan < 0 {
		return 0, newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := n.policies[id]
	if !ok {
		return 0, newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return 0, newErr(ErrTerminated, "已终态")
	}
	if p.hasPending() {
		return 0, newErr(ErrHasPendingEndorsement, "存在未生效批改")
	}
	var refund int64
	if p.now < p.effDate+p.hesitation {
		refund = p.totalPaid() - p.issueFee
		if refund < 0 {
			refund = 0
		}
	} else {
		refund = p.cashValue(p.now, loan)
	}
	p.terminated = true
	return refund, nil
}

func (n *naiveEngine) partialSurrender(id string, reduce, loan int64) (int64, error) {
	if reduce <= 0 || loan < 0 {
		return 0, newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := n.policies[id]
	if !ok {
		return 0, newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return 0, newErr(ErrTerminated, "已终态")
	}
	if reduce >= p.sumInsured {
		return 0, newErr(ErrInvalidParam, "参数非法")
	}
	if p.now < p.effDate+p.hesitation {
		return 0, newErr(ErrInHesitation, "犹豫期内不允许部分退保")
	}
	if p.hasPending() {
		return 0, newErr(ErrHasPendingEndorsement, "存在未生效批改")
	}
	newSumInsured := p.sumInsured - reduce
	if newSumInsured < p.minSumInsured {
		return 0, newErr(ErrBelowMinSumInsured, "低于最低保额")
	}
	refund := p.cashValue(p.now, loan) * reduce / p.sumInsured
	p.premium = (p.premium*newSumInsured + p.sumInsured - 1) / p.sumInsured
	p.sumInsured = newSumInsured
	return refund, nil
}

func (n *naiveEngine) snapshot(id string) Snapshot {
	p := n.policies[id]
	snap := Snapshot{
		Now:          p.now,
		SumInsured:   p.sumInsured,
		Premium:      p.premium,
		TotalPaid:    p.totalPaid(),
		PaymentTerm:  p.paymentTerm,
		Beneficiary:  p.beneficiary,
		Terminated:   p.terminated,
		Endorsements: make(map[string]EndorsementSnapshot, len(p.endorsements)),
	}
	for _, en := range p.endorsements {
		snap.Endorsements[en.id] = EndorsementSnapshot{State: en.state, TopUp: en.topUp}
	}
	return snap
}

var errCodeNames = map[ErrCode]string{
	ErrInvalidParam:          "参数非法",
	ErrPolicyNotFound:        "保单不存在",
	ErrClockRegression:       "时钟回退",
	ErrTerminated:            "已终态",
	ErrEndorsementNotFound:   "批改不存在",
	ErrEndorsementDuplicate:  "批改重复",
	ErrAlreadyEffective:      "已生效",
	ErrRetroactive:           "追溯批改",
	ErrPendingTopUp:          "待补缴",
	ErrHasPendingEndorsement: "存在未生效批改",
	ErrInHesitation:          "犹豫期内",
	ErrAlreadyPaid:           "已缴",
	ErrBelowMinSumInsured:    "低于最低保额",
}

func describeErr(err error) string {
	if err == nil {
		return "ok"
	}
	if e, ok := errOf(err); ok {
		return errCodeNames[e.Code]
	}
	return err.Error()
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ea, oka := errOf(a)
	eb, okb := errOf(b)
	return oka && okb && ea.Code == eb.Code
}

func randomPolicyInput(r *rand.Rand, id string) PolicyInput {
	sumInsured := int64(1000 + r.Intn(20000))
	ratios := make([]int64, 1+r.Intn(3))
	for i := range ratios {
		ratios[i] = int64(r.Intn(151))
	}
	return PolicyInput{
		ID:              id,
		EffectiveDate:   int64(r.Intn(20)),
		AnnualPremium:   int64(1 + r.Intn(2000)),
		SumInsured:      sumInsured,
		MinSumInsured:   int64(1 + r.Intn(int(sumInsured))),
		HesitationDays:  int64(r.Intn(30)),
		IssueFee:        int64(r.Intn(50)),
		CashValueRatios: ratios,
		PaymentTerm:     1 + r.Intn(30),
		Beneficiary:     "b0",
	}
}

// 与朴素模型对照随机批改、缴费、推进与退保序列，
// 日志打印每步输入、双方输出与判定依据（拒绝原因）。
func TestFuzzAgainstNaiveModel(t *testing.T) {
	endorsementIDs := []string{"e0", "e1", "e2", "e3", "e4", "e5"}
	for seed := int64(1); seed <= 30; seed++ {
		r := rand.New(rand.NewSource(seed))
		eng := NewEngine()
		nav := newNaiveEngine()
		var policyIDs []string
		inputs := make(map[string]PolicyInput)
		registerOne := func() string {
			id := fmt.Sprintf("p%d", len(policyIDs))
			in := randomPolicyInput(r, id)
			inputs[id] = in
			if err := eng.RegisterPolicy(in); err != nil {
				t.Fatalf("seed %d register: %v", seed, err)
			}
			nav.register(in)
			policyIDs = append(policyIDs, id)
			return id
		}
		registerOne()
		registerOne()
		for op := 0; op < 400; op++ {
			id := policyIDs[r.Intn(len(policyIDs))]
			if r.Intn(50) == 0 {
				id = "ghost" // 偶发不存在的保单号
			}
			snap, _ := eng.Snapshot(id)
			if snap.Terminated && r.Intn(2) == 0 {
				id = registerOne() // 已终态则以新保单继续，保持有效路径覆盖
				snap, _ = eng.Snapshot(id)
			}
			effDate := inputs[id].EffectiveDate
			curYear := int64(0)
			if snap.Now >= effDate {
				curYear = (snap.Now - effDate) / 365
			}
			var desc string
			var engErr, navErr error
			var engVal, navVal any
			switch r.Intn(8) {
			case 0: // 缴费
				year := curYear + int64(r.Intn(3)) - 1
				amount := snap.Premium
				if r.Intn(10) < 3 {
					amount += int64(r.Intn(3)) - 1
				}
				desc = fmt.Sprintf("pay year=%d amount=%d", year, amount)
				engErr = eng.PayPremium(id, year, amount)
				navErr = nav.payPremium(id, year, amount)
			case 1: // 预约批改
				in := EndorsementInput{
					ID:       endorsementIDs[r.Intn(len(endorsementIDs))],
					Type:     EndorsementType(1 + r.Intn(3)),
					ApplyDay: snap.Now + int64(r.Intn(5)) - 2,
					EffDay:   snap.Now + int64(r.Intn(12)) - 2,
				}
				switch in.Type {
				case EndorsementSumInsured:
					in.NewSumInsured = int64(1 + r.Intn(30000))
				case EndorsementPaymentTerm:
					in.NewTerm = r.Intn(3)
				case EndorsementBeneficiary:
					in.Beneficiary = fmt.Sprintf("b%d", r.Intn(1000))
				}
				desc = fmt.Sprintf("schedule %+v", in)
				engErr = eng.ScheduleEndorsement(id, in)
				navErr = nav.schedule(id, in)
			case 2: // 撤销批改
				eid := endorsementIDs[r.Intn(len(endorsementIDs))]
				desc = "cancel " + eid
				engErr = eng.CancelEndorsement(id, eid)
				navErr = nav.cancel(id, eid)
			case 3: // 补缴到账
				eid := endorsementIDs[r.Intn(len(endorsementIDs))]
				desc = "topup " + eid
				engVal, engErr = eng.PayTopUp(id, eid)
				navVal, navErr = nav.payTopUp(id, eid)
			case 4: // 时刻推进
				day := snap.Now + int64(r.Intn(30)) - 2
				if r.Intn(4) == 0 {
					day = snap.Now + int64(r.Intn(800)) // 跨保单年度推进
				}
				desc = fmt.Sprintf("advance day=%d", day)
				engVal, engErr = eng.Advance(id, day)
				navVal, navErr = nav.advance(id, day)
			case 5: // 现金价值
				day := snap.Now + int64(r.Intn(400)) - 5
				loan := int64(r.Intn(3000))
				desc = fmt.Sprintf("cashvalue day=%d loan=%d", day, loan)
				engVal, engErr = eng.CashValue(id, day, loan)
				navVal, navErr = nav.cashValueQuery(id, day, loan)
			case 6: // 整单退保
				loan := int64(r.Intn(3000))
				desc = fmt.Sprintf("surrender loan=%d", loan)
				engVal, engErr = eng.Surrender(id, loan)
				navVal, navErr = nav.surrender(id, loan)
			case 7: // 部分退保
				reduce := int64(r.Intn(int(snap.SumInsured) + 1))
				loan := int64(r.Intn(3000))
				desc = fmt.Sprintf("partial reduce=%d loan=%d", reduce, loan)
				engVal, engErr = eng.PartialSurrender(id, reduce, loan)
				navVal, navErr = nav.partialSurrender(id, reduce, loan)
			}
			match := sameErr(engErr, navErr) && reflect.DeepEqual(engVal, navVal)
			t.Logf("seed=%d op=%d policy=%s now=%d action=%s => engine=(%v,%s) naive=(%v,%s) match=%v",
				seed, op, id, snap.Now, desc, engVal, describeErr(engErr), navVal, describeErr(navErr), match)
			if !match {
				t.Fatalf("seed=%d op=%d %s: engine=(%v,%v) naive=(%v,%v)",
					seed, op, desc, engVal, engErr, navVal, navErr)
			}
			for _, pid := range policyIDs {
				engSnap, _ := eng.Snapshot(pid)
				navSnap := nav.snapshot(pid)
				if !reflect.DeepEqual(engSnap, navSnap) {
					t.Fatalf("seed=%d op=%d policy=%s snapshot mismatch:\nengine=%+v\nnaive=%+v",
						seed, op, pid, engSnap, navSnap)
				}
			}
		}
	}
}

// buildHistory 构造带 years 年缴费与批改历史的保单。
func buildHistory(tb testing.TB, years int) *Engine {
	tb.Helper()
	e := NewEngine()
	in := baseInput("p")
	in.CashValueRatios = []int64{10, 20, 30}
	if err := e.RegisterPolicy(in); err != nil {
		tb.Fatal(err)
	}
	for y := 0; y < years; y++ {
		day := int64(y) * 365
		if err := e.PayPremium("p", int64(y), 1000); err != nil {
			tb.Fatalf("pay year %d: %v", y, err)
		}
		if err := e.ScheduleEndorsement("p", EndorsementInput{
			ID: fmt.Sprintf("e%d", y), Type: EndorsementBeneficiary,
			ApplyDay: day, EffDay: day, Beneficiary: fmt.Sprintf("b%d", y),
		}); err != nil {
			tb.Fatalf("schedule %d: %v", y, err)
		}
		if _, err := e.Advance("p", day+365); err != nil {
			tb.Fatalf("advance %d: %v", y, err)
		}
	}
	return e
}

// 现金价值计算开销不随历史已生效批改数与缴费记录数增长。
func BenchmarkCashValueLargeHistory(b *testing.B) {
	e := buildHistory(b, 2000)
	day := int64(2000) * 365
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.CashValue("p", day, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// 时刻推进中找下一条应生效批改为堆顶 O(1) 判定，与历史规模无关。
func BenchmarkAdvanceLargeHistory(b *testing.B) {
	e := buildHistory(b, 2000)
	now := int64(2000) * 365
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Advance("p", now+int64(i)); err != nil {
			b.Fatal(err)
		}
	}
}

// 可验证证明：10 年历史与 2000 年历史的单次操作耗时之比不得明显增长。
func TestConstantTimeScaling(t *testing.T) {
	measure := func(years int) (cashValue, advance time.Duration) {
		e := buildHistory(t, years)
		now := int64(years) * 365
		const rounds = 20000
		start := time.Now()
		for i := 0; i < rounds; i++ {
			if _, err := e.CashValue("p", now, 0); err != nil {
				t.Fatal(err)
			}
		}
		cashValue = time.Since(start)
		start = time.Now()
		for i := 0; i < rounds; i++ {
			if _, err := e.Advance("p", now+int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		advance = time.Since(start)
		return cashValue, advance
	}
	cvSmall, advSmall := measure(10)
	cvLarge, advLarge := measure(2000)
	t.Logf("cashvalue: 10y=%v 2000y=%v ratio=%.2f", cvSmall, cvLarge, float64(cvLarge)/float64(cvSmall))
	t.Logf("advance:   10y=%v 2000y=%v ratio=%.2f", advSmall, advLarge, float64(advLarge)/float64(advSmall))
	if cvLarge > cvSmall*5 {
		t.Fatalf("cash value cost grows with history: %v -> %v", cvSmall, cvLarge)
	}
	if advLarge > advSmall*5 {
		t.Fatalf("advance cost grows with history: %v -> %v", advSmall, advLarge)
	}
}
