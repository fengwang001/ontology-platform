package insurance

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

// mPolicy 是朴素模型中的保单。
type mPolicy struct {
	Policy
	seq        int
	cancelled  bool
	cancelDate int64
}

// mAccident 是朴素模型中的事故。
type mAccident struct {
	Accident
	seq int
}

// model 是独立实现的朴素对照模型：每次变更后都从第一条事故开始
// 完整重放全部事故，用直接、低效的循环实现规则，不做任何增量优化。
type model struct {
	seq         int
	policies    map[string]*mPolicy
	policyOrder []*mPolicy
	accidents   map[string]*mAccident
	order       []*mAccident
	results     map[string]AccidentResult
	paid        map[string]int64
}

func newModel() *model {
	return &model{
		policies:  make(map[string]*mPolicy),
		accidents: make(map[string]*mAccident),
		results:   make(map[string]AccidentResult),
		paid:      make(map[string]int64),
	}
}

func (m *model) addPolicy(p Policy) error {
	if p.ID == "" || p.Subject == "" || p.Insurer == "" || p.SumInsured <= 0 ||
		p.Deductible < 0 || p.Effective < 0 || p.Expiry < p.Effective ||
		(p.Clause != ClauseNormal && p.Clause != ClauseExcess) {
		return &Error{Kind: ErrKindInvalidParam}
	}
	if _, ok := m.policies[p.ID]; ok {
		return &Error{Kind: ErrKindDuplicateID}
	}
	mp := &mPolicy{Policy: p, seq: m.seq}
	m.seq++
	m.policies[p.ID] = mp
	m.policyOrder = append(m.policyOrder, mp)
	m.replay()
	return nil
}

func (m *model) registerAccident(a Accident) (AccidentResult, error) {
	if a.ID == "" || a.Subject == "" || a.Date < 0 || a.Loss < 0 {
		return AccidentResult{}, &Error{Kind: ErrKindInvalidParam}
	}
	if _, ok := m.accidents[a.ID]; ok {
		return AccidentResult{}, &Error{Kind: ErrKindDuplicateID}
	}
	acc := &mAccident{Accident: a, seq: m.seq}
	m.seq++
	m.accidents[a.ID] = acc
	m.order = append(m.order, acc)
	m.replay()
	return m.results[a.ID], nil
}

func (m *model) correctAccident(id string, newLoss int64) error {
	if newLoss < 0 {
		return &Error{Kind: ErrKindInvalidParam}
	}
	acc, ok := m.accidents[id]
	if !ok {
		return &Error{Kind: ErrKindNotFound}
	}
	acc.Loss = newLoss
	m.replay()
	return nil
}

func (m *model) cancelPolicy(id string, date int64, insurer string) error {
	if id == "" || date < 0 {
		return &Error{Kind: ErrKindInvalidParam}
	}
	p, ok := m.policies[id]
	if !ok {
		return &Error{Kind: ErrKindNotFound}
	}
	if insurer != p.Insurer {
		return &Error{Kind: ErrKindInvalidParam}
	}
	if p.cancelled {
		return &Error{Kind: ErrKindAlreadyCancelled}
	}
	for _, acc := range m.order {
		covered := p.seq < acc.seq && acc.Subject == p.Subject &&
			p.Effective <= acc.Date && acc.Date <= p.Expiry
		if covered && date <= acc.Date {
			return &Error{Kind: ErrKindInvalidParam}
		}
	}
	p.cancelled = true
	p.cancelDate = date
	m.replay()
	return nil
}

// replay 从头完整重放所有事故，重算每个结果与每张保单累计赔付。
func (m *model) replay() {
	m.paid = make(map[string]int64, len(m.policies))
	m.results = make(map[string]AccidentResult, len(m.order))
	for _, acc := range m.order {
		var cover []*mPolicy
		for _, p := range m.policyOrder {
			if p.seq >= acc.seq || p.Subject != acc.Subject {
				continue
			}
			if acc.Date < p.Effective || acc.Date > p.Expiry {
				continue
			}
			if p.cancelled && acc.Date >= p.cancelDate {
				continue
			}
			cover = append(cover, p)
		}
		m.results[acc.ID] = naiveSettle(acc, cover, m.paid)
	}
}

type mPart struct {
	p     *mPolicy
	indep int64
}

// naiveSettle 用直接循环结算一起事故并累加各保单赔付。
func naiveSettle(acc *mAccident, cover []*mPolicy, paid map[string]int64) AccidentResult {
	var normals, excesses []mPart
	for _, p := range cover {
		indep := acc.Loss - p.Deductible
		if indep < 0 {
			indep = 0
		}
		if rem := p.SumInsured - paid[p.ID]; indep > rem {
			indep = rem
		}
		if p.Clause == ClauseNormal {
			normals = append(normals, mPart{p, indep})
		} else {
			excesses = append(excesses, mPart{p, indep})
		}
	}
	var sumN int64
	for _, pt := range normals {
		sumN += pt.indep
	}
	normalTotal := sumN
	if acc.Loss < normalTotal {
		normalTotal = acc.Loss
	}
	nShares := naiveAllocate(normals, normalTotal)
	uncomp := acc.Loss - normalTotal
	var sumE int64
	for _, pt := range excesses {
		sumE += pt.indep
	}
	excessTotal := sumE
	if uncomp < excessTotal {
		excessTotal = uncomp
	}
	eShares := naiveAllocate(excesses, excessTotal)

	share := make(map[string]int64)
	for i, pt := range normals {
		share[pt.p.ID] = nShares[i]
	}
	for i, pt := range excesses {
		share[pt.p.ID] = eShares[i]
	}
	payouts := make([]Payout, 0, len(cover))
	var total int64
	for _, p := range cover {
		amt := share[p.ID]
		payouts = append(payouts, Payout{PolicyID: p.ID, Amount: amt})
		paid[p.ID] += amt
		total += amt
	}
	sort.Slice(payouts, func(i, j int) bool { return payouts[i].PolicyID < payouts[j].PolicyID })
	return AccidentResult{AccidentID: acc.ID, Seq: acc.seq, Loss: acc.Loss, TotalPaid: total, Payouts: payouts}
}

// naiveAllocate 朴素实现比例分摊与余数规则。
func naiveAllocate(parts []mPart, total int64) []int64 {
	shares := make([]int64, len(parts))
	if total <= 0 {
		return shares
	}
	var sum int64
	for _, pt := range parts {
		sum += pt.indep
	}
	if sum == 0 {
		return shares
	}
	var got int64
	for i, pt := range parts {
		shares[i] = pt.indep * total / sum
		got += shares[i]
	}
	rem := total - got
	idx := make([]int, len(parts))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		pa, pb := parts[idx[a]].p, parts[idx[b]].p
		if pa.Effective != pb.Effective {
			return pa.Effective < pb.Effective
		}
		return pa.ID < pb.ID
	})
	for rem > 0 {
		for _, i := range idx {
			if rem == 0 {
				break
			}
			if parts[i].indep > shares[i] {
				shares[i]++
				rem--
			}
		}
	}
	return shares
}

// errKind 提取错误类别字符串用于比较与日志。
func errKind(err error) string {
	if err == nil {
		return "nil"
	}
	if k, ok := KindOf(err); ok {
		return k.String()
	}
	return "foreign:" + err.Error()
}

// TestRandomOpsAgainstModel 用大量随机操作序列对照优化实现与朴素模型，
// 每步打印输入、输出与判定依据。
func TestRandomOpsAgainstModel(t *testing.T) {
	const seeds = 12
	const opsPerSeed = 300
	for seed := int64(1); seed <= seeds; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := randSource(seed)
			sys := NewSystem()
			m := newModel()
			subjects := []string{"car", "house", "boat"}
			insurers := []string{"insA", "insB"}
			var policyIDs, accidentIDs []string
			pCount, aCount := 0, 0

			compareAll := func(op int) {
				t.Helper()
				for _, id := range accidentIDs {
					got, ok := sys.AccidentResultOf(id)
					if !ok {
						t.Fatalf("op %d: system lost accident %s", op, id)
					}
					want := m.results[id]
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("op %d: accident %s mismatch\n system=%+v\n model =%+v", op, id, got, want)
					}
					if got.TotalPaid > got.Loss {
						t.Fatalf("op %d: accident %s total %d exceeds loss %d", op, id, got.TotalPaid, got.Loss)
					}
				}
				for _, id := range policyIDs {
					st, ok := sys.PolicyStatusOf(id)
					if !ok {
						t.Fatalf("op %d: system lost policy %s", op, id)
					}
					mp := m.policies[id]
					if st.TotalPaid != m.paid[id] || st.Remaining != mp.SumInsured-m.paid[id] ||
						st.Cancelled != mp.cancelled || st.CancelDate != mp.cancelDate {
						t.Fatalf("op %d: policy %s mismatch\n system=%+v\n model paid=%d cancelled=%v date=%d",
							op, id, st, m.paid[id], mp.cancelled, mp.cancelDate)
					}
					if st.TotalPaid > st.Policy.SumInsured {
						t.Fatalf("op %d: policy %s paid %d exceeds sum insured %d", op, id, st.TotalPaid, st.Policy.SumInsured)
					}
				}
			}

			for op := 0; op < opsPerSeed; op++ {
				switch rng.Intn(10) {
				case 0, 1, 2: // 新增保单（含偶发非法/重复）
					p := Policy{
						ID:         fmt.Sprintf("P%d", pCount),
						Subject:    subjects[rng.Intn(len(subjects))],
						SumInsured: int64(1 + rng.Intn(200)),
						Deductible: int64(rng.Intn(40)),
						Effective:  int64(rng.Intn(20)),
						Insurer:    insurers[rng.Intn(len(insurers))],
						Clause:     ClauseType(rng.Intn(2)),
					}
					p.Expiry = p.Effective + int64(rng.Intn(20))
					if rng.Intn(12) == 0 {
						p.SumInsured = 0 // 非法参数
					}
					if rng.Intn(15) == 0 && len(policyIDs) > 0 {
						p.ID = policyIDs[rng.Intn(len(policyIDs))] // 重复编号
					}
					sysErr := sys.AddPolicy(p)
					modelErr := m.addPolicy(p)
					t.Logf("op=%d addPolicy input=%+v sys=%s model=%s basis=error-kind-equality", op, p, errKind(sysErr), errKind(modelErr))
					if errKind(sysErr) != errKind(modelErr) {
						t.Fatalf("op %d: addPolicy error mismatch: sys=%v model=%v", op, sysErr, modelErr)
					}
					if sysErr == nil {
						policyIDs = append(policyIDs, p.ID)
						pCount++
					}

				case 3, 4, 5, 6: // 登记事故（含偶发非法/重复）
					a := Accident{
						ID:      fmt.Sprintf("A%d", aCount),
						Subject: subjects[rng.Intn(len(subjects))],
						Date:    int64(rng.Intn(30)),
						Loss:    int64(rng.Intn(150)),
					}
					if rng.Intn(15) == 0 {
						a.Loss = -1 // 非法参数
					}
					if rng.Intn(15) == 0 && len(accidentIDs) > 0 {
						a.ID = accidentIDs[rng.Intn(len(accidentIDs))] // 重复编号
					}
					sysRes, sysErr := sys.RegisterAccident(a)
					modelRes, modelErr := m.registerAccident(a)
					t.Logf("op=%d registerAccident input=%+v sysErr=%s modelErr=%s sysTotal=%d modelTotal=%d basis=error-kind-and-result-equality",
						op, a, errKind(sysErr), errKind(modelErr), sysRes.TotalPaid, modelRes.TotalPaid)
					if errKind(sysErr) != errKind(modelErr) {
						t.Fatalf("op %d: registerAccident error mismatch: sys=%v model=%v", op, sysErr, modelErr)
					}
					if sysErr == nil {
						if !reflect.DeepEqual(sysRes, modelRes) {
							t.Fatalf("op %d: registerAccident result mismatch\n system=%+v\n model =%+v", op, sysRes, modelRes)
						}
						accidentIDs = append(accidentIDs, a.ID)
						aCount++
					}

				case 7: // 更正事故（含不存在）
					id := fmt.Sprintf("A%d", rng.Intn(aCount+3))
					if len(accidentIDs) > 0 && rng.Intn(4) != 0 {
						id = accidentIDs[rng.Intn(len(accidentIDs))]
					}
					newLoss := int64(rng.Intn(150))
					sysErr := sys.CorrectAccident(id, newLoss)
					modelErr := m.correctAccident(id, newLoss)
					t.Logf("op=%d correctAccident id=%s newLoss=%d sys=%s model=%s basis=error-kind-equality",
						op, id, newLoss, errKind(sysErr), errKind(modelErr))
					if errKind(sysErr) != errKind(modelErr) {
						t.Fatalf("op %d: correctAccident error mismatch: sys=%v model=%v", op, sysErr, modelErr)
					}

				case 8: // 注销保单（含不存在/重复注销/错误保险人/非法注销日）
					id := fmt.Sprintf("P%d", rng.Intn(pCount+3))
					if len(policyIDs) > 0 && rng.Intn(4) != 0 {
						id = policyIDs[rng.Intn(len(policyIDs))]
					}
					insurer := insurers[rng.Intn(len(insurers))]
					date := int64(rng.Intn(35))
					sysErr := sys.CancelPolicy(id, date, insurer)
					modelErr := m.cancelPolicy(id, date, insurer)
					t.Logf("op=%d cancelPolicy id=%s date=%d insurer=%s sys=%s model=%s basis=error-kind-equality",
						op, id, date, insurer, errKind(sysErr), errKind(modelErr))
					if errKind(sysErr) != errKind(modelErr) {
						t.Fatalf("op %d: cancelPolicy error mismatch: sys=%v model=%v", op, sysErr, modelErr)
					}

				case 9: // 只读查询一致性抽查
					if len(accidentIDs) > 0 {
						id := accidentIDs[rng.Intn(len(accidentIDs))]
						got, _ := sys.AccidentResultOf(id)
						want := m.results[id]
						t.Logf("op=%d query accident=%s sysTotal=%d modelTotal=%d basis=result-equality", op, id, got.TotalPaid, want.TotalPaid)
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("op %d: query mismatch for %s\n system=%+v\n model =%+v", op, id, got, want)
						}
					}
				}
				compareAll(op)
			}
			t.Logf("seed=%d finished: %d policies, %d accidents, all results match the naive model", seed, len(policyIDs), len(accidentIDs))
		})
	}
}
