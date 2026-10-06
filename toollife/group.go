package toollife

import "sync"

// holder 一把刀的可变状态。所有字段仅在所属 group 的互斥锁内访问。
type holder struct {
	id       string
	status   ToolStatus
	used     int
	reserved int
	// warned 是否已发出过寿命预警；换新后重置。
	warned bool
}

func (h *holder) remaining(limit int) int {
	r := limit - h.used
	if r < 0 {
		r = 0
	}
	return r
}

// pickReason 选刀失败原因。
type pickReason int

const (
	pickOK       pickReason = iota
	pickNoMargin            // 存在未耗尽的刀，但余量全部被预占占用（预占释放后可能成功）
	pickNoTool              // 其余无刀情况：没有刀 / 全部破损、锁定、耗尽
)

// group 一个刀组的全部状态；mu 保护本结构内所有字段。
type group struct {
	mu    sync.Mutex
	id    string
	cfg   GroupConfig
	tools []*holder
	byID  map[string]*holder

	// requests 已受理的申请台账（申请编号 -> 记录）。
	requests map[string]*request
}

// request 一次申请的生命周期记录。
type request struct {
	id        string
	groupID   string
	estimated int
	toolID    string
	// open 为 true 时预占仍然有效；记账或中止后变 false。
	open bool
	// settled 是否已记账（幂等用）。
	settled bool
	actual  int
	// exhaustedAtSettle 记账时刻的结果快照，供换新删除旧编号后幂等回放。
	exhaustedAtSettle bool
}

// canCarry 判断该刀在指定模式下能否承载一次预计消耗为 est 的申请。
//
// 共同前提：刀具必须可用（破损/已耗尽/已锁定均不可选）。
//   - 严格：used+reserved+est <= limit（预占后仍可承载）；
//   - 宽松：used+reserved < limit（申请时未占满即可，本次允许超出上限）。
func canCarry(h *holder, limit int, mode SelectMode, est int) bool {
	if h.status != StatusAvailable {
		return false
	}
	switch mode {
	case Strict:
		return h.used+h.reserved+est <= limit
	case Lenient:
		return h.used+h.reserved < limit
	default:
		return false
	}
}

// pick 按顺序选第一把满足条件的刀；失败时给出区分原因。
//
// 失败原因区分（判定依据）：
//   - 只要存在一把「未耗尽（status 可用且 used < limit）但本次模式下无法
//     承载」的刀，判 pickNoMargin——余量被预占占满，预占释放后可能再成功；
//   - 否则（没有刀，或所有刀均破损/锁定/已耗尽）判 pickNoTool。
func (g *group) pick(est int) (*holder, pickReason) {
	marginExists := false
	for _, h := range g.tools {
		if canCarry(h, g.cfg.LifeLimit, g.cfg.Mode, est) {
			return h, pickOK
		}
		if h.status == StatusAvailable && h.used < g.cfg.LifeLimit {
			// 刀本身未耗尽，只是当前被预占顶满（严格模式 est 过大，
			// 或两种模式下 reserved 占去了全部余量）。
			marginExists = true
		}
	}
	if marginExists {
		return nil, pickNoMargin
	}
	return nil, pickNoTool
}

// currentPick 查询口径：当前第一把「可用且 used+reserved < 上限」的刀。
// 与申请预计消耗无关，仅回答「此刻组里下一把会轮到谁」。
func (g *group) currentPick() *holder {
	for _, h := range g.tools {
		if h.status == StatusAvailable && h.used+h.reserved < g.cfg.LifeLimit {
			return h
		}
	}
	return nil
}

func newGroup(id string, cfg GroupConfig) *group {
	g := &group{
		id:       id,
		cfg:      cfg,
		byID:     make(map[string]*holder, len(cfg.ToolIDs)),
		requests: make(map[string]*request),
	}
	for _, tid := range cfg.ToolIDs {
		h := &holder{id: tid, status: StatusAvailable}
		g.tools = append(g.tools, h)
		g.byID[tid] = h
	}
	return g
}

// warnPermille 已用占寿命上限的千分比（向下取整）。
func usedPermille(used, limit int) int { return used * 1000 / limit }
