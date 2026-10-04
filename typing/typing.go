package typing

import "ontology/bloodstock"

type State uint8

const (
	Unknown State = iota
	Single
	Confirmed
	Disputed // 存疑（粘滞），仅主管 Resolve 可解除
)

func (s State) String() string {
	switch s {
	case Single:
		return "单次"
	case Confirmed:
		return "已确认"
	case Disputed:
		return "存疑"
	default:
		return "未知"
	}
}

type Result struct {
	ABO bloodstock.ABO
	Rh  bloodstock.Rh
}

type Record struct {
	State State
	Type  Result // State=Single/Confirmed 时有效
	Count int
}

// Book 鉴定记录。非并发安全，由 issue.Manager 上锁调用。
type Book struct {
	patients map[string]*pRec
	samples  map[string]struct{}
}

type pRec struct {
	state State
	t     Result
	count int
}

func NewBook() *Book {
	return &Book{
		patients: map[string]*pRec{},
		samples:  map[string]struct{}{},
	}
}

// Type 记录一次鉴定；标本号重复返回 bloodstock.ErrDuplicate（归类为「不存在/重复」）。
// 当患者因本次结果与既有结果不一致而转为存疑时，disputed=true，
// 调用方须立即释放该患者全部预留。
// 存疑为粘滞状态：处于存疑时，新结果不再改变状态（保持存疑），disputed 只在
// 「本次首次跌入存疑」时为 true；Resolve 之后再次出现不一致则重新跌入存疑。
func (b *Book) Type(patient, sample string, abo bloodstock.ABO, rh bloodstock.Rh) (disputed bool, err error) {
	if _, dup := b.samples[sample]; dup {
		return false, bloodstock.ErrDuplicate
	}
	b.samples[sample] = struct{}{}
	p := b.patients[patient]
	if p == nil {
		p = &pRec{}
		b.patients[patient] = p
	}
	p.count++
	switch p.state {
	case Unknown:
		p.state = Single
		p.t = Result{ABO: abo, Rh: rh}
		return false, nil
	case Single, Confirmed:
		if p.t.ABO == abo && p.t.Rh == rh {
			if p.state == Single && p.count >= 2 {
				p.state = Confirmed
			}
			return false, nil
		}
		p.state = Disputed
		p.t = Result{}
		return true, nil
	default: // Disputed：粘滞
		return false, nil
	}
}

// Resolve 主管裁定，转为已确认；患者无任何鉴定记录返回 ErrNotFound。
func (b *Book) Resolve(patient string, abo bloodstock.ABO, rh bloodstock.Rh) error {
	p := b.patients[patient]
	if p == nil || p.count == 0 {
		return bloodstock.ErrNotFound
	}
	p.state = Confirmed
	p.t = Result{ABO: abo, Rh: rh}
	return nil
}

func (b *Book) Get(patient string) Record {
	p := b.patients[patient]
	if p == nil {
		return Record{State: Unknown}
	}
	return Record{State: p.state, Type: p.t, Count: p.count}
}

// HasSample 标本号是否已使用（用于落地前重复检查）。
func (b *Book) HasSample(sample string) bool {
	_, ok := b.samples[sample]
	return ok
}

// Snapshot 返回全部有鉴定记录患者的副本。
func (b *Book) Snapshot() map[string]Record {
	out := make(map[string]Record, len(b.patients))
	for k, p := range b.patients {
		out[k] = Record{State: p.state, Type: p.t, Count: p.count}
	}
	return out
}

func (b *Book) Groups(patient string) []bloodstock.Group {
	return GroupsFor(b.Get(patient))
}

// aboOrder 已确认受者的 ABO 可用次序（不含 Rh 展开）。
func aboOrder(a bloodstock.ABO) []bloodstock.ABO {
	switch a {
	case bloodstock.A:
		return []bloodstock.ABO{bloodstock.A, bloodstock.O}
	case bloodstock.B:
		return []bloodstock.ABO{bloodstock.B, bloodstock.O}
	case bloodstock.AB:
		return []bloodstock.ABO{bloodstock.AB, bloodstock.A, bloodstock.B, bloodstock.O}
	default:
		return []bloodstock.ABO{bloodstock.O}
	}
}

// GroupsFor 按鉴定状态计算可用血型次序。
//   - 已确认：ABO 次序内每个 ABO 先阳后阴（ABO 优先于 Rh）。
//   - 单次：只用 O，Rh 规则按该次结果（阳性：O 阳、O 阴；阴性：O 阴）。
//   - 未知/存疑：只用 O 阴。
func GroupsFor(rec Record) []bloodstock.Group {
	switch rec.State {
	case Confirmed:
		as := aboOrder(rec.Type.ABO)
		g := make([]bloodstock.Group, 0, 2*len(as))
		for _, a := range as {
			if rec.Type.Rh == bloodstock.RhPos {
				g = append(g, bloodstock.Group{ABO: a, Rh: bloodstock.RhPos})
			}
			g = append(g, bloodstock.Group{ABO: a, Rh: bloodstock.RhNeg})
		}
		return g
	case Single:
		if rec.Type.Rh == bloodstock.RhPos {
			return []bloodstock.Group{
				{ABO: bloodstock.O, Rh: bloodstock.RhPos},
				{ABO: bloodstock.O, Rh: bloodstock.RhNeg},
			}
		}
		return []bloodstock.Group{{ABO: bloodstock.O, Rh: bloodstock.RhNeg}}
	default:
		return []bloodstock.Group{{ABO: bloodstock.O, Rh: bloodstock.RhNeg}}
	}
}
