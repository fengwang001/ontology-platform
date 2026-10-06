package registry

import (
	"fmt"
	"io"
	"sort"
)

// NaiveRegistry 为与 Registry 同规则的独立朴素实现：
// 每次操作全量扫描证书，撤销选择靠排序完成，不与主实现共享任何索引代码，
// 用于随机操作序列下的差分对照。
type NaiveRegistry struct {
	cfg    Config
	trace  io.Writer
	certs  map[int64]*Cert
	facs   map[string]*nFac
	cons   map[string]map[int64]*conPeriod
	serial int64
	cseq   int64
	events []Event
}

type nFac struct {
	holder    string
	start     int64
	end       int64
	rem       int64
	maxIssued int64
	periods   map[int64]*nPeriod
}

type nPeriod struct {
	qty       int64
	inRem     int64
	outRem    int64
	active    int
	everIssue bool
}

// NewNaive 创建朴素模型。
func NewNaive(cfg Config, trace io.Writer) *NaiveRegistry {
	if cfg.UnitQty <= 0 {
		cfg.UnitQty = 1
	}
	if cfg.MaxAgePeriods < 0 {
		cfg.MaxAgePeriods = 0
	}
	if cfg.MinPeriod == 0 && cfg.MaxPeriod == 0 {
		cfg.MinPeriod = 1
		cfg.MaxPeriod = 1_000_000
	}
	return &NaiveRegistry{
		cfg:   cfg,
		trace: trace,
		certs: map[int64]*Cert{},
		facs:  map[string]*nFac{},
		cons:  map[string]map[int64]*conPeriod{},
	}
}

func (n *NaiveRegistry) validPeriod(p int64) bool {
	return p >= n.cfg.MinPeriod && p <= n.cfg.MaxPeriod
}

func (n *NaiveRegistry) emit(e Event) { n.events = append(n.events, e) }

func (n *NaiveRegistry) logf(format string, args ...any) {
	if n.trace != nil {
		fmt.Fprintf(n.trace, "[naive] "+format+"\n", args...)
	}
}

func (n *NaiveRegistry) con(id string) map[int64]*conPeriod {
	c := n.cons[id]
	if c == nil {
		c = map[int64]*conPeriod{}
		n.cons[id] = c
	}
	return c
}

func (f *nFac) qualified(p int64) bool {
	return p >= f.start && (f.end == 0 || p < f.end)
}

// RegisterFacility 同 Registry。
func (n *NaiveRegistry) RegisterFacility(id, holder string, startPeriod int64) *Error {
	if id == "" || holder == "" || !n.validPeriod(startPeriod) {
		return errf(CodeInvalidParam, "设施/持有人为空或生效期越界")
	}
	if n.facs[id] != nil {
		return errf(CodeInvalidParam, "设施已登记，资格生效期不可更改")
	}
	n.facs[id] = &nFac{holder: holder, start: startPeriod, periods: map[int64]*nPeriod{}}
	n.logf("RegisterFacility(%q,%q,%d) ok", id, holder, startPeriod)
	return nil
}

// SetQualificationEnd 同 Registry。
func (n *NaiveRegistry) SetQualificationEnd(id string, endPeriod int64) *Error {
	f := n.facs[id]
	if f == nil || !n.validPeriod(endPeriod) {
		return errf(CodeInvalidParam, "设施不存在或终止期越界")
	}
	if endPeriod < f.start || endPeriod < f.end {
		return errf(CodeInvalidParam, "终止期不得早于生效期或提前于既有终止期")
	}
	if f.maxIssued != 0 && endPeriod <= f.maxIssued {
		return errf(CodeConflictIssued, "终止期不得早于已核发最大发电期的下一期")
	}
	f.end = endPeriod
	n.emit(Event{Kind: EventQualEndSet, Facility: id, EndPeriod: endPeriod})
	return nil
}

// RevokeQualificationEnd 同 Registry。
func (n *NaiveRegistry) RevokeQualificationEnd(id string) *Error {
	f := n.facs[id]
	if f == nil {
		return errf(CodeInvalidParam, "设施不存在")
	}
	f.end = 0
	n.emit(Event{Kind: EventQualEndRevoked, Facility: id})
	return nil
}

// Remainder 同 Registry。
func (n *NaiveRegistry) Remainder(id string) (int64, *Error) {
	f := n.facs[id]
	if f == nil {
		return 0, errf(CodeInvalidParam, "设施不存在")
	}
	return f.rem, nil
}

// CancelledQty 同 Registry。
func (n *NaiveRegistry) CancelledQty(consumer string, usePeriod int64) int64 {
	cp := n.cons[consumer][usePeriod]
	if cp == nil {
		return 0
	}
	return cp.cancelled
}

// Cert 同 Registry。
func (n *NaiveRegistry) Cert(s int64) *Cert {
	c := n.certs[s]
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// CertCount 同 Registry。
func (n *NaiveRegistry) CertCount() int { return len(n.certs) }

// LastSerial 同 Registry。
func (n *NaiveRegistry) LastSerial() int64 { return n.serial }

// Events 同 Registry。
func (n *NaiveRegistry) Events() []Event { return append([]Event(nil), n.events...) }

// RegisterMeter 同 Registry。
func (n *NaiveRegistry) RegisterMeter(id string, genPeriod, qty int64) *Error {
	f := n.facs[id]
	if f == nil || !n.validPeriod(genPeriod) || qty < 0 {
		return errf(CodeInvalidParam, "设施不存在、期编号越界或电量为负")
	}
	if !f.qualified(genPeriod) {
		return errf(CodeOutsideQualification, "发电期不在资格区间内")
	}
	p := f.periods[genPeriod]
	if p == nil {
		p = &nPeriod{inRem: f.rem}
		f.periods[genPeriod] = p
	}
	p.qty = qty
	want := int((qty + p.inRem) / n.cfg.UnitQty)

	if p.active > want {
		n.shrink(id, genPeriod, p, p.active-want)
	} else if want > p.active {
		n.grow(f, id, genPeriod, p, want-p.active)
	}
	p.outRem = p.qty + p.inRem - int64(p.active)*n.cfg.UnitQty
	f.rem = p.outRem
	if p.everIssue && genPeriod > f.maxIssued {
		f.maxIssued = genPeriod
	}
	n.emit(Event{Kind: EventMeterRegistered, Facility: id, Period: genPeriod, Qty: qty, Remainder: p.outRem})
	return nil
}

func (n *NaiveRegistry) grow(f *nFac, id string, genPeriod int64, p *nPeriod, k int) {
	for i := 0; i < k; i++ {
		n.serial++
		s := n.serial
		n.certs[s] = &Cert{Serial: s, Facility: id, Period: genPeriod, Holder: f.holder, Status: StatusHeld}
		p.active++
		p.everIssue = true
		n.emit(Event{Kind: EventIssued, Facility: id, Period: genPeriod, Serial: s, Holder: f.holder})
	}
}

// shrink 全量收集该期非撤销证书：先持有序号降序，后已注销按注销时刻降序。
func (n *NaiveRegistry) shrink(id string, genPeriod int64, p *nPeriod, k int) {
	var held, cancelled []int64
	for s, c := range n.certs {
		if c.Facility != id || c.Period != genPeriod {
			continue
		}
		switch c.Status {
		case StatusHeld:
			held = append(held, s)
		case StatusCancelled:
			cancelled = append(cancelled, s)
		}
	}
	sort.Slice(held, func(i, j int) bool { return held[i] > held[j] })
	sort.Slice(cancelled, func(i, j int) bool {
		return n.certs[cancelled[i]].CancelSeq > n.certs[cancelled[j]].CancelSeq
	})
	ordered := append(append([]int64{}, held...), cancelled...)
	for i := 0; i < k; i++ {
		s := ordered[i]
		c := n.certs[s]
		if c.Status == StatusHeld {
			c.Status = StatusRevoked
			n.emit(Event{Kind: EventRevokedHeld, Serial: s, Facility: id, Period: genPeriod})
		} else {
			n.con(c.Consumer)[c.UsePeriod].cancelled -= n.cfg.UnitQty
			c.Status = StatusRevoked
			n.emit(Event{Kind: EventDeclarationInvalidated, Consumer: c.Consumer,
				UsePeriod: c.UsePeriod, Serial: s, Facility: id, Period: genPeriod})
		}
		p.active--
	}
}

// Transfer 同 Registry。
func (n *NaiveRegistry) Transfer(from, to string, serials []int64) *Error {
	if e := batchParams(from, to, serials); e != nil {
		return e
	}
	ordered := append([]int64(nil), serials...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, s := range ordered {
		c := n.certs[s]
		if c == nil {
			return errAt(CodeInvalidParam, s, "证书序号不存在")
		}
		if c.Status != StatusHeld {
			return errAt(CodeStateNotAllowed, s, "证书非持有状态")
		}
		if c.Holder != from {
			return errAt(CodeNotHolder, s, "证书非转让方持有")
		}
	}
	for _, s := range ordered {
		n.certs[s].Holder = to
	}
	n.emit(Event{Kind: EventTransferred, Holder: from, ToHolder: to, Serials: ordered})
	return nil
}

// RegisterConsumption 同 Registry。
func (n *NaiveRegistry) RegisterConsumption(id string, usePeriod, qty int64) *Error {
	if id == "" || !n.validPeriod(usePeriod) || qty < 0 {
		return errf(CodeInvalidParam, "用电方为空、期编号越界或电量为负")
	}
	c := n.con(id)
	cp := c[usePeriod]
	if cp == nil {
		cp = &conPeriod{qty: -1}
		c[usePeriod] = cp
	}
	if qty < cp.cancelled {
		return errf(CodeBelowCancelled, "修正电量低于已注销总量")
	}
	cp.qty = qty
	n.emit(Event{Kind: EventConsumptionRegistered, Consumer: id, UsePeriod: usePeriod, Qty: qty})
	return nil
}

// Declare 同 Registry。
func (n *NaiveRegistry) Declare(consumer string, usePeriod int64, serials []int64) *Error {
	if e := declareParams(consumer, usePeriod, n.validPeriod, serials); e != nil {
		return e
	}
	ordered := append([]int64(nil), serials...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	earliest := usePeriod - n.cfg.MaxAgePeriods
	for _, s := range ordered {
		c := n.certs[s]
		if c == nil {
			return errAt(CodeInvalidParam, s, "证书序号不存在")
		}
		if c.Status != StatusHeld {
			return errAt(CodeStateNotAllowed, s, "证书非持有状态")
		}
		if c.Holder != consumer {
			return errAt(CodeNotHolder, s, "证书非该用电方持有")
		}
		if c.Period > usePeriod || c.Period < earliest {
			return errAt(CodePeriodMismatch, s, "发电期与用电期期限不符")
		}
	}
	c := n.con(consumer)
	cp := c[usePeriod]
	registered, cancelled := int64(0), int64(0)
	if cp != nil {
		registered, cancelled = cp.qty, cp.cancelled
	}
	add := int64(len(ordered)) * n.cfg.UnitQty
	if registered < 0 || cancelled+add > registered {
		return errf(CodeOverConsumption, "注销总量将超过登记用电量")
	}
	for _, s := range ordered {
		n.cseq++
		cert := n.certs[s]
		cert.Status = StatusCancelled
		cert.Consumer = consumer
		cert.UsePeriod = usePeriod
		cert.CancelSeq = n.cseq
		n.emit(Event{Kind: EventCancelled, Serial: s, Consumer: consumer, UsePeriod: usePeriod,
			Facility: cert.Facility, Period: cert.Period})
	}
	if cp == nil {
		cp = &conPeriod{qty: registered}
		c[usePeriod] = cp
	}
	cp.cancelled += add
	return nil
}

// batchParams 与主实现批级参数规则一致，但独立实现。
func batchParams(from, to string, serials []int64) *Error {
	if len(serials) == 0 {
		return errf(CodeInvalidParam, "批数量非正")
	}
	if from == "" || to == "" {
		return errf(CodeInvalidParam, "持有人为空")
	}
	if from == to {
		return errf(CodeInvalidParam, "自转让非法")
	}
	seen := map[int64]struct{}{}
	for _, s := range serials {
		if s <= 0 {
			return errAt(CodeInvalidParam, s, "证书序号非正")
		}
		if _, ok := seen[s]; ok {
			return errAt(CodeInvalidParam, s, "批内序号重复")
		}
		seen[s] = struct{}{}
	}
	return nil
}

func declareParams(consumer string, usePeriod int64, valid func(int64) bool, serials []int64) *Error {
	if consumer == "" || !valid(usePeriod) {
		return errf(CodeInvalidParam, "用电方为空或用电期越界")
	}
	if len(serials) == 0 {
		return errf(CodeInvalidParam, "批数量非正")
	}
	seen := map[int64]struct{}{}
	for _, s := range serials {
		if s <= 0 {
			return errAt(CodeInvalidParam, s, "证书序号非正")
		}
		if _, ok := seen[s]; ok {
			return errAt(CodeInvalidParam, s, "批内序号重复")
		}
		seen[s] = struct{}{}
	}
	return nil
}
