// Package naive 是刀具寿命管理规则的独立朴素参照实现。
//
// 它刻意不引用生产包 toollife，以直白、无并发、无性能技巧的方式
// 把同一份规约重写一遍，供差分测试（differential testing）对照：
// 对随机多通道操作序列，生产服务的结果必须与本模型的某个串行重放一致，
// 且最终状态必须等于该串行重放的最终状态。
package naive

// Status 朴素模型的刀具状态（与生产包各自独立定义）。
type Status int

const (
	Avail Status = iota
	Broken
	Exhausted
	Locked
)

// Mode 选刀模式。
type Mode int

const (
	Strict Mode = iota
	Lenient
)

// Cfg 刀组配置。
type Cfg struct {
	Limit uint64
	Warn  uint32
	Mode  Mode
}

type tool struct {
	id       string
	status   Status
	used     uint64
	reserved uint64
	warned   bool
}

// ReqKind 操作类别。
type ReqKind int

const (
	KApply ReqKind = iota
	KSettle
	KAbort
	KBroken
	KReplace
	KLock
	KUnlock
)

// Op 是串行序列中的一条操作。
type Op struct {
	Kind    ReqKind
	Group   string
	Tool    string
	NewTool string
	Req     string
	Amount  uint64
}

// Result 是一条操作的朴素结果。
type Result struct {
	OK     bool
	Code   string // invalid|group|tool|conflict|state|reqnotfound|notool|nocapacity
	ToolID string
}

// Warning 朴素模型的预警记录。
type Warning struct {
	Group string
	Tool  string
}

type reqRec struct {
	group string
	est   uint64
	tool  *tool
	state int // 0 open, 1 settled, 2 aborted
}

// Model 朴素模型：单线程，一把互斥锁都不需要。
type Model struct {
	groups map[string]*group
	reqs   map[string]*reqRec
	warn   []Warning
}

// New 创建空模型并登记一个刀组。
func New(gid string, cfg Cfg, toolIDs []string) *Model {
	m := &Model{
		groups: map[string]*group{},
		reqs:   map[string]*reqRec{},
	}
	g := &group{cfg: cfg, byID: map[string]*tool{}}
	for _, id := range toolIDs {
		t := &tool{id: id, status: Avail}
		g.order = append(g.order, t)
		g.byID[id] = t
	}
	m.groups[gid] = g
	return m
}

type group struct {
	cfg   Cfg
	order []*tool
	byID  map[string]*tool
}

func bad(code string) Result { return Result{OK: false, Code: code} }

func (m *Model) carry(t *tool, cfg Cfg, est uint64) bool {
	committed := t.used + t.reserved
	if cfg.Mode == Lenient {
		return committed < cfg.Limit
	}
	return committed <= cfg.Limit && est <= cfg.Limit-committed
}

// choose 按顺序选刀；返回 (刀, "ok"|"notool"|"nocapacity")。
func (m *Model) choose(g *group, est uint64) (*tool, string) {
	for _, t := range g.order {
		if t.status == Avail && m.carry(t, g.cfg, est) {
			return t, "ok"
		}
	}
	if len(g.order) == 0 {
		return nil, "notool"
	}
	for _, t := range g.order {
		if t.status != Avail || t.reserved == 0 {
			return nil, "notool"
		}
		c := *t
		c.reserved = 0
		if !m.carry(&c, g.cfg, est) {
			return nil, "notool"
		}
	}
	return nil, "nocapacity"
}

func permille(used, limit uint64) uint64 {
	if limit == 0 {
		if used == 0 {
			return 0
		}
		return 1000
	}
	return used * 1000 / limit
}

// Exec 按给定顺序执行一条操作。
func (m *Model) Exec(op Op) Result {
	g, ok := m.groups[op.Group]
	switch op.Kind {
	case KApply:
		if op.Req == "" || op.Amount == 0 {
			return bad("invalid")
		}
		if !ok {
			return bad("group")
		}
		if r, dup := m.reqs[op.Req]; dup {
			if r.group != op.Group || r.est != op.Amount {
				return bad("conflict")
			}
			return Result{OK: true, ToolID: r.tool.id}
		}
		t, why := m.choose(g, op.Amount)
		if why != "ok" {
			return bad(why)
		}
		t.reserved += op.Amount
		m.reqs[op.Req] = &reqRec{group: op.Group, est: op.Amount, tool: t}
		return Result{OK: true, ToolID: t.id}

	case KSettle:
		if op.Req == "" {
			return bad("invalid")
		}
		r, found := m.reqs[op.Req]
		if !found {
			return bad("reqnotfound")
		}
		if r.state == 1 {
			return Result{OK: true}
		}
		if r.state == 2 {
			return bad("state")
		}
		t := r.tool
		cfg := m.groups[r.group].cfg
		before := permille(t.used, cfg.Limit)
		t.reserved -= r.est
		t.used += op.Amount
		r.state = 1
		if t.status == Avail && t.used >= cfg.Limit {
			t.status = Exhausted
		}
		after := permille(t.used, cfg.Limit)
		if !t.warned && before < uint64(cfg.Warn) && after >= uint64(cfg.Warn) {
			t.warned = true
			m.warn = append(m.warn, Warning{Group: r.group, Tool: t.id})
		}
		return Result{OK: true}

	case KAbort:
		if op.Req == "" {
			return bad("invalid")
		}
		r, found := m.reqs[op.Req]
		if !found {
			return bad("reqnotfound")
		}
		if r.state == 2 {
			return Result{OK: true}
		}
		if r.state == 1 {
			return bad("state")
		}
		r.tool.reserved -= r.est
		r.state = 2
		return Result{OK: true}

	case KBroken:
		if op.Group == "" || op.Tool == "" {
			return bad("invalid")
		}
		if !ok {
			return bad("group")
		}
		t, found := g.byID[op.Tool]
		if !found {
			return bad("tool")
		}
		switch t.status {
		case Broken:
			return Result{OK: true}
		case Avail, Locked:
			t.status = Broken
			return Result{OK: true}
		default:
			return bad("state")
		}

	case KReplace:
		if op.Group == "" || op.Tool == "" || op.NewTool == "" || op.Tool == op.NewTool {
			return bad("invalid")
		}
		if !ok {
			return bad("group")
		}
		t, found := g.byID[op.Tool]
		if !found {
			return bad("tool")
		}
		if t.status != Broken {
			return bad("state")
		}
		if t.reserved != 0 {
			return bad("state")
		}
		if _, dup := g.byID[op.NewTool]; dup {
			return bad("invalid")
		}
		nt := &tool{id: op.NewTool, status: Avail}
		for i, cur := range g.order {
			if cur == t {
				g.order[i] = nt
			}
		}
		delete(g.byID, op.Tool)
		g.byID[op.NewTool] = nt
		return Result{OK: true}

	case KLock:
		if op.Group == "" || op.Tool == "" {
			return bad("invalid")
		}
		if !ok {
			return bad("group")
		}
		t, found := g.byID[op.Tool]
		if !found {
			return bad("tool")
		}
		switch t.status {
		case Locked:
			return Result{OK: true}
		case Avail:
			t.status = Locked
			return Result{OK: true}
		default:
			return bad("state")
		}

	case KUnlock:
		if op.Group == "" || op.Tool == "" {
			return bad("invalid")
		}
		if !ok {
			return bad("group")
		}
		t, found := g.byID[op.Tool]
		if !found {
			return bad("tool")
		}
		switch t.status {
		case Avail:
			return Result{OK: true}
		case Locked:
			if t.used >= g.cfg.Limit {
				return bad("state")
			}
			t.status = Avail
			return Result{OK: true}
		default:
			return bad("state")
		}
	}
	return bad("invalid")
}

// ToolState 是朴素模型的单刀最终状态。
type ToolState struct {
	ID       string
	Status   Status
	Used     uint64
	Reserved uint64
}

// State 返回刀组内刀具按顺序的最终状态。
func (m *Model) State(gid string) []ToolState {
	g := m.groups[gid]
	out := make([]ToolState, 0, len(g.order))
	for _, t := range g.order {
		out = append(out, ToolState{ID: t.id, Status: t.status, Used: t.used, Reserved: t.reserved})
	}
	return out
}

// Warnings 返回预警序列。
func (m *Model) Warnings() []Warning {
	out := make([]Warning, len(m.warn))
	copy(out, m.warn)
	return out
}
