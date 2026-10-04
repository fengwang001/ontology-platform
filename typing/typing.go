package typing

import "errors"

import "ontology/bloodstock"

type Kind int

const (
	Unknown Kind = iota
	Single
	Confirmed
	Disputed
)

type Result struct {
	ABO bloodstock.ABO
	Rh  bloodstock.Rh
}

var ErrSampleDup = errors.New("typing: duplicate sample")

type record struct {
	at     int64
	sample string
	r      Result
}

type patient struct {
	records []record
	kind    Kind
	// 一致结果（Single/Confirmed/Resolved 时有效）。
	res Result
	// Resolve 裁定后的结果。
	resolved *Result
}

type Registry struct {
	patients map[string]*patient
	samples  map[string]bool
}

func NewRegistry() *Registry {
	return &Registry{patients: map[string]*patient{}, samples: map[string]bool{}}
}

// Type 记录一次鉴定。标本号重复返回 ErrSampleDup（不改变状态）。
// 出现与此前不一致结果即转为存疑（粘滞，后续一致也不恢复）。
func (r *Registry) Type(now int64, patientName, sample string, abo bloodstock.ABO, rh bloodstock.Rh) (Kind, bool) {
	p := r.patients[patientName]
	if p == nil {
		p = &patient{}
		r.patients[patientName] = p
	}
	cur := Result{ABO: abo, Rh: rh}
	if len(p.records) == 0 {
		resolvedResult := p.res
		hadResolve := p.resolved != nil
		p.records = append(p.records, record{at: now, sample: sample, r: cur})
		if hadResolve {
			// 裁定后第一次鉴定：裁定不与鉴定次数叠加。
			// 一致 -> 单次；不一致 -> 立即存疑（粘滞），裁定失效并释放预留。
			p.resolved = nil
			if cur != resolvedResult {
				p.kind = Disputed
				return Disputed, true
			}
			p.res = cur
			p.kind = Single
			return Single, false
		}
		p.res = cur
		p.kind = Single
		return Single, false
	}
	if p.kind == Disputed {
		// 已存疑：粘滞，仅追加记录，结果不可能重新一致。
		p.records = append(p.records, record{at: now, sample: sample, r: cur})
		return Disputed, cur != p.records[0].r
	}
	if cur != p.res {
		p.kind = Disputed
		p.records = append(p.records, record{at: now, sample: sample, r: cur})
		return Disputed, true
	}
	p.records = append(p.records, record{at: now, sample: sample, r: cur})
	p.kind = Confirmed
	return Confirmed, false
}

// Resolve 主管裁定，转为已确认（清除存疑粘滞）。
func (r *Registry) Resolve(patientName string, abo bloodstock.ABO, rh bloodstock.Rh) Kind {
	p := r.patients[patientName]
	if p == nil {
		p = &patient{}
		r.patients[patientName] = p
	}
	res := Result{ABO: abo, Rh: rh}
	p.resolved = &res
	p.res = res
	p.kind = Confirmed
	// 裁定重置鉴定历史：此后第一次鉴定进入「与裁定比较」分支。
	p.records = nil
	return Confirmed
}

// State 返回患者状态；不存在时为 Unknown。
func (r *Registry) State(patientName string) (Kind, Result) {
	p := r.patients[patientName]
	if p == nil {
		return Unknown, Result{}
	}
	return p.kind, p.res
}

// Order 返回患者当前可用血袋的血型槽位次序（见 bloodstock.Slot）。
// 未知/存疑 -> O 阴；单次 -> O 型按 Rh；已确认 -> ABO 相容次序，ABO 优先于 Rh。
func (r *Registry) Order(patientName string) []int {
	p := r.patients[patientName]
	if p == nil || p.kind == Unknown {
		return []int{bloodstock.Slot(bloodstock.O, bloodstock.Negative)}
	}
	if p.kind == Disputed {
		return []int{bloodstock.Slot(bloodstock.O, bloodstock.Negative)}
	}
	if p.kind == Single {
		if p.res.Rh == bloodstock.Negative {
			return []int{bloodstock.Slot(bloodstock.O, bloodstock.Negative)}
		}
		return []int{
			bloodstock.Slot(bloodstock.O, bloodstock.Positive),
			bloodstock.Slot(bloodstock.O, bloodstock.Negative),
		}
	}
	// Confirmed：ABO 次序内先阳后阴。
	var aboOrder []bloodstock.ABO
	switch p.res.ABO {
	case bloodstock.A:
		aboOrder = []bloodstock.ABO{bloodstock.A, bloodstock.O}
	case bloodstock.B:
		aboOrder = []bloodstock.ABO{bloodstock.B, bloodstock.O}
	case bloodstock.AB:
		aboOrder = []bloodstock.ABO{bloodstock.AB, bloodstock.A, bloodstock.B, bloodstock.O}
	case bloodstock.O:
		aboOrder = []bloodstock.ABO{bloodstock.O}
	}
	out := make([]int, 0, 2*len(aboOrder))
	if p.res.Rh == bloodstock.Negative {
		for _, a := range aboOrder {
			out = append(out, bloodstock.Slot(a, bloodstock.Negative))
		}
		return out
	}
	for _, a := range aboOrder {
		out = append(out, bloodstock.Slot(a, bloodstock.Positive))
		out = append(out, bloodstock.Slot(a, bloodstock.Negative))
	}
	return out
}

func (r *Registry) PutSample(sample string) { r.samples[sample] = true }

func (r *Registry) KnownSample(sample string) bool { return r.samples[sample] }
