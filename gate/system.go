package gate

import (
	"fmt"
	"sort"
	"sync"
)

type gateState struct {
	id       string
	maxLevel Level
	kind     GateKind
	adj      []string // 升序去重
	adjSet   map[string]bool
	tree     itree
}

// segment 为航班在某个登机口上的一段占用；gate 为空串表示待分配。
type segment struct {
	kind       SegKind
	start, end int
	gate       string
	owner      *flightState
}

func (sg *segment) key() segKey {
	return segKey{start: sg.start, fid: sg.owner.f.ID, seg: sg.kind}
}

type flightState struct {
	f    Flight
	segs map[SegKind]*segment
}

// System 为登机口分配系统。所有方法可并发调用，
// 内部由单互斥锁串行化，结果等价于某串行执行顺序。
type System struct {
	mu      sync.Mutex
	cfg     Config
	gates   map[string]*gateState
	flights map[string]*flightState
	now     int   // 上一次被接受操作的时刻
	visits  int64 // Treap 节点访问计数（性能可验证性）
}

// NewSystem 校验配置并创建系统。
func NewSystem(cfg Config) (*System, error) {
	if cfg.Buffer < 0 || cfg.StayLimit <= 0 || cfg.Deplane <= 0 || cfg.Board <= 0 {
		return nil, fmt.Errorf("invalid config: %+v", cfg)
	}
	if cfg.Deplane+cfg.Board > cfg.StayLimit {
		return nil, fmt.Errorf("deplane+board exceeds stay limit: %+v", cfg)
	}
	return &System{
		cfg:     cfg,
		gates:   map[string]*gateState{},
		flights: map[string]*flightState{},
	}, nil
}

// accept 在接受一次变更后推进时钟。
func (s *System) accept(now int) {
	s.now = now
}

// pruneAll 物理清除所有登机口上 end <= now 的已过期占用（左闭右开，
// 端点可复用）。每个变更操作在校验时钟后、判定前调用，保证判定
// 只看到当前时钟下仍然有效的占用。
func (s *System) pruneAll(now int) {
	for _, g := range s.gates {
		g.tree.deleteExpired(now, &s.visits)
	}
}

// AddGate 注册登机口；相邻关系自动对称。
func (s *System) AddGate(spec GateSpec, now int) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || spec.ID == "" || spec.MaxLevel < 1 || spec.MaxLevel > MaxLevel ||
		spec.Kind < GateInternational || spec.Kind > GateDual {
		return reject(ReasonInvalidParam, "bad gate spec %+v", spec)
	}
	if now < s.now {
		return reject(ReasonClockSkew, "now=%d < clock=%d", now, s.now)
	}
	s.pruneAll(now)
	if _, dup := s.gates[spec.ID]; dup {
		return reject(ReasonInvalidParam, "gate %s already exists", spec.ID)
	}
	adjSet := map[string]bool{}
	for _, a := range spec.Adjacent {
		if a == spec.ID {
			return reject(ReasonInvalidParam, "gate %s adjacent to itself", spec.ID)
		}
		if _, ok := s.gates[a]; !ok {
			return reject(ReasonNotFound, "adjacent gate %s not found", a)
		}
		adjSet[a] = true
	}
	g := &gateState{id: spec.ID, maxLevel: spec.MaxLevel, kind: spec.Kind, adjSet: adjSet}
	for a := range adjSet {
		g.adj = append(g.adj, a)
		ag := s.gates[a]
		if !ag.adjSet[spec.ID] {
			ag.adjSet[spec.ID] = true
			ag.adj = append(ag.adj, spec.ID)
			sort.Strings(ag.adj)
		}
	}
	sort.Strings(g.adj)
	s.gates[spec.ID] = g
	s.accept(now)
	return okResult("gate %s added (maxLevel=%d kind=%d adj=%v)", spec.ID, spec.MaxLevel, spec.Kind, g.adj)
}

// AddFlight 注册航班，当前时刻初始等于计划时刻，全部分段待分配。
func (s *System) AddFlight(spec FlightSpec, now int) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || spec.ID == "" || spec.Level < 1 || spec.Level > MaxLevel ||
		spec.Kind < FlightInternational || spec.Kind > FlightDomestic ||
		spec.SchedArr < 0 || spec.SchedDep < spec.SchedArr || spec.BoardLead < 0 {
		return reject(ReasonInvalidParam, "bad flight spec %+v", spec)
	}
	if now < s.now {
		return reject(ReasonClockSkew, "now=%d < clock=%d", now, s.now)
	}
	s.pruneAll(now)
	if _, dup := s.flights[spec.ID]; dup {
		return reject(ReasonInvalidParam, "flight %s already exists", spec.ID)
	}
	fs := &flightState{f: Flight{
		ID: spec.ID, Level: spec.Level, Kind: spec.Kind,
		SchedArr: spec.SchedArr, SchedDep: spec.SchedDep, BoardLead: spec.BoardLead,
		CurArr: spec.SchedArr, CurDep: spec.SchedDep,
	}}
	fs.segs = buildSegs(fs, fs.f.CurArr, fs.f.CurDep, s.cfg, nil)
	s.flights[spec.ID] = fs
	s.accept(now)
	return okResult("flight %s added (level=%d kind=%d sched=[%d,%d])",
		spec.ID, spec.Level, spec.Kind, spec.SchedArr, spec.SchedDep)
}

// splitStay 判断占用区间长度是否超过停留上限（恰等于不拆）。
func splitStay(arr, dep int, cfg Config) bool {
	return dep+cfg.Buffer-arr > cfg.StayLimit
}

// buildSegs 计算航班在给定时刻下的分段，登机口按规则继承：
// 整段→拆分：卸客段继承原登机口，登机段待分配；
// 拆分→整段：整段继承卸客段登机口；其余各段保持原登机口。
func buildSegs(fs *flightState, arr, dep int, cfg Config, old map[SegKind]*segment) map[SegKind]*segment {
	gateOf := func(k SegKind) string {
		if old != nil {
			if sg, ok := old[k]; ok {
				return sg.gate
			}
		}
		return ""
	}
	mk := func(k SegKind, st, en int, gate string) *segment {
		return &segment{kind: k, start: st, end: en, gate: gate, owner: fs}
	}
	out := map[SegKind]*segment{}
	if splitStay(arr, dep, cfg) {
		dg := gateOf(SegDeplane)
		if _, ok := old[SegDeplane]; !ok {
			dg = gateOf(SegWhole)
		}
		out[SegDeplane] = mk(SegDeplane, arr, arr+cfg.Deplane, dg)
		out[SegBoard] = mk(SegBoard, dep-cfg.Board, dep+cfg.Buffer, gateOf(SegBoard))
	} else {
		g := gateOf(SegWhole)
		if _, ok := old[SegWhole]; !ok {
			g = gateOf(SegDeplane)
		}
		out[SegWhole] = mk(SegWhole, arr, dep+cfg.Buffer, g)
	}
	return out
}

func kindCompat(fk FlightKind, gk GateKind) bool {
	return gk == GateDual || (fk == FlightInternational) == (gk == GateInternational)
}

// higherPriority 比较撤离优先级：国际先于国内、等级高者先、
// 计划到达早者先、标识小者先。
func higherPriority(a, b *Flight) bool {
	if a.Kind != b.Kind {
		return a.Kind == FlightInternational
	}
	if a.Level != b.Level {
		return a.Level > b.Level
	}
	if a.SchedArr != b.SchedArr {
		return a.SchedArr < b.SchedArr
	}
	return a.ID < b.ID
}

// protectedNow 判断航班是否已到达或已开始登机（不可被挤占）。
func protectedNow(arr, dep, boardLead, now int) bool {
	return now >= arr || now >= dep-boardLead
}

// Occupant 查询某登机口在时刻 t 的占用者。
// 已过期（end <= 当前时钟）的历史占用已被物理清除，查询开销
// 不随该登机口历史占用总数增长。
func (s *System) Occupant(gateID string, t int) (string, SegKind, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.gates[gateID]
	if !ok {
		return "", SegWhole, false
	}
	g.tree.deleteExpired(s.now, &s.visits)
	for _, sg := range g.tree.overlap(t, t+1, &s.visits) {
		return sg.owner.f.ID, sg.kind, true
	}
	return "", SegWhole, false
}

// SnapFlight 为航班状态快照，用于结果比对。
type SnapFlight struct {
	CurArr int
	CurDep int
	Gates  map[SegKind]string // 每段的登机口，空串为待分配
}

// Snapshot 返回全部航班的当前状态。
func (s *System) Snapshot() map[string]SnapFlight {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]SnapFlight{}
	for id, fs := range s.flights {
		sf := SnapFlight{CurArr: fs.f.CurArr, CurDep: fs.f.CurDep, Gates: map[SegKind]string{}}
		for k, sg := range fs.segs {
			sf.Gates[k] = sg.gate
		}
		out[id] = sf
	}
	return out
}

// CheckConsistency 校验当前全部指派同时满足兼容、相邻与互不冲突。
// 与判定语义一致：end <= 当前时钟的已过期占用不参与检查。
func (s *System) CheckConsistency() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, fs := range s.flights {
		for _, sg := range fs.segs {
			if sg.gate == "" || sg.end <= s.now {
				continue
			}
			g := s.gates[sg.gate]
			if fs.f.Level > g.maxLevel {
				return fmt.Errorf("flight %s level %d exceeds gate %s max %d",
					fs.f.ID, fs.f.Level, g.id, g.maxLevel)
			}
			if !kindCompat(fs.f.Kind, g.kind) {
				return fmt.Errorf("flight %s kind mismatch with gate %s", fs.f.ID, g.id)
			}
			if tc := s.timeConflict(fs, g, sg.start, sg.end); tc != nil {
				return fmt.Errorf("flight %s overlaps flight %s at gate %s",
					fs.f.ID, tc.owner.f.ID, g.id)
			}
			if adj := s.adjacencyConflict(fs, g, sg.start, sg.end); adj != nil {
				return fmt.Errorf("flight %s adjacency violation with flight %s at gate %s",
					fs.f.ID, adj.owner.f.ID, adj.gate)
			}
		}
	}
	return nil
}
