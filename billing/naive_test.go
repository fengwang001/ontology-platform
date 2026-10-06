package billing

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// naiveSlot 是朴素模型中单个槽位的完整状态。
type naiveSlot struct {
	ingress, egress int64
	version         int64 // 0 表示当前缺失
	highestSeen     int64
}

// NaiveSettler 是与规格逐条对应的独立朴素实现：
// 每次查询都把全部有效槽位拷贝出来排序，作为 Treap 实现的对照。
type NaiveSettler struct {
	n       int
	slots   []naiveSlot
	settled bool
	result  SettlementResult
}

func NewNaiveSettler(n int) *NaiveSettler {
	return &NaiveSettler{n: n, slots: make([]naiveSlot, n)}
}

func naiveMax(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (m *NaiveSettler) validValues() []int64 {
	var vals []int64
	for _, sl := range m.slots {
		if sl.version != 0 {
			vals = append(vals, naiveMax(sl.ingress, sl.egress))
		}
	}
	return vals
}

// naiveRate 返回计费速率；K=0 时返回 CodeNoSamples。
func (m *NaiveSettler) naiveRate() (int64, error) {
	vals := m.validValues()
	k := len(vals)
	if k == 0 {
		return 0, &Error{Code: CodeNoSamples}
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] > vals[j] })
	drop := k * 5 / 100
	return vals[drop], nil
}

func (m *NaiveSettler) Submit(s Sample) error {
	if s.Slot < 0 || s.Slot >= m.n || s.Ingress < 0 || s.Ingress > MaxRate ||
		s.Egress < 0 || s.Egress > MaxRate || s.Version <= 0 {
		return &Error{Code: CodeInvalidArgument}
	}
	if m.settled {
		return &Error{Code: CodeSettled}
	}
	cur := &m.slots[s.Slot]
	if cur.version != 0 {
		switch {
		case s.Version > cur.highestSeen:
			cur.ingress, cur.egress = s.Ingress, s.Egress
			cur.version = s.Version
			cur.highestSeen = s.Version
			return nil
		case s.Version == cur.version:
			if cur.ingress == s.Ingress && cur.egress == s.Egress {
				return nil
			}
			return &Error{Code: CodeVersionConflict}
		default:
			return &Error{Code: CodeStaleSample}
		}
	}
	if s.Version <= cur.highestSeen {
		return &Error{Code: CodeStaleSample}
	}
	cur.ingress, cur.egress = s.Ingress, s.Egress
	cur.version = s.Version
	cur.highestSeen = s.Version
	return nil
}

func (m *NaiveSettler) Withdraw(slot int, version int64) error {
	if slot < 0 || slot >= m.n || version <= 0 {
		return &Error{Code: CodeInvalidArgument}
	}
	if m.settled {
		return &Error{Code: CodeSettled}
	}
	cur := &m.slots[slot]
	if cur.version == 0 {
		return &Error{Code: CodeNotFound}
	}
	if cur.version != version {
		return &Error{Code: CodeVersionMismatch}
	}
	cur.ingress, cur.egress = 0, 0
	cur.version = 0
	return nil
}

func (m *NaiveSettler) Rate() (int64, error) { return m.naiveRate() }

func (m *NaiveSettler) Overview() Overview {
	k := len(m.validValues())
	o := Overview{ReceivedSlots: k, MissingSlots: m.n - k, Settled: m.settled}
	if rate, err := m.naiveRate(); err == nil {
		o.BilledRate, o.RateDefined = rate, true
	}
	return o
}

func (m *NaiveSettler) Settle(committed int64, tol int) (SettlementResult, error) {
	if committed < 0 || committed > MaxRate || tol < 0 || tol > MaxBasisPt {
		return SettlementResult{}, &Error{Code: CodeInvalidArgument}
	}
	if m.settled {
		r := m.result
		r.Repeated = true
		return r, nil
	}
	k := len(m.validValues())
	missing := m.n - k
	if k == 0 {
		return SettlementResult{}, &Error{Code: CodeNoSamples}
	}
	if int64(missing)*MaxBasisPt > int64(tol)*int64(m.n) {
		return SettlementResult{}, &Error{Code: CodeInsufficientData}
	}
	rate, _ := m.naiveRate()
	r := SettlementResult{
		BilledRate:    rate,
		Base:          naiveMax(committed, rate),
		ValidSlots:    k,
		MissingSlots:  missing,
		CommittedRate: committed,
		ToleranceBp:   tol,
	}
	m.settled = true
	m.result = r
	return r, nil
}

func cmpResult(a, b SettlementResult) string {
	var diffs []string
	pairs := []struct {
		name string
		x, y any
	}{
		{"BilledRate", a.BilledRate, b.BilledRate},
		{"Base", a.Base, b.Base},
		{"ValidSlots", a.ValidSlots, b.ValidSlots},
		{"MissingSlots", a.MissingSlots, b.MissingSlots},
		{"Repeated", a.Repeated, b.Repeated},
		{"CommittedRate", a.CommittedRate, b.CommittedRate},
		{"ToleranceBp", a.ToleranceBp, b.ToleranceBp},
	}
	for _, p := range pairs {
		if fmt.Sprint(p.x) != fmt.Sprint(p.y) {
			diffs = append(diffs, fmt.Sprintf("%s %v!=%v", p.name, p.x, p.y))
		}
	}
	return strings.Join(diffs, "; ")
}

type traceLogger struct{ sb strings.Builder }

func (l *traceLogger) line(format string, args ...any) {
	fmt.Fprintf(&l.sb, format+"\n", args...)
}

func (l *traceLogger) String() string { return l.sb.String() }

func errSig(err error) string {
	if err == nil {
		return "ok"
	}
	var be *Error
	if errors.As(err, &be) {
		return be.Code.String()
	}
	return "other:" + err.Error()
}
