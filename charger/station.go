// Package charger 实现充电站功率分配与排队控制。
//
// 典型用法：
//
//	st, _ := charger.NewStation(100, []charger.Port{
//	    {ID: "A", Cap: 40}, {ID: "B", Cap: 30},
//	})
//	id, _ := st.Plug(charger.PlugRequest{
//	    At: 0, PortID: "A", Need: 200, MaxP: 40, MinP: 10,
//	})
//	st.SetCap(20, 80)
//	events, _ := st.AdvanceTo(60)
//	charged, _ := st.Unplug(id, 60)
//
// 所有公开方法可并发调用；时间为整数秒，功率/电量为整数单位。
// 精确规则见 DESIGN.md。
package charger

import (
	"fmt"
	"sort"
	"sync"
)

// capChange 是一次未来生效的总上限变更。
type capChange struct {
	at  int64
	cap int
	seq int64
}

// Station 是一座充电站。所有公开方法可并发调用，
// 内部由单一互斥锁串行化，结果等价于某种串行执行顺序。
type Station struct {
	mu       sync.Mutex
	now      int64
	totalCap int
	ports    map[string]*Port
	portSess map[string]*Session // 接口当前会话（充满/充电/等待均占用；拔枪后删除）
	sess     map[int64]*Session
	nextID   int64
	changes  []capChange // 未来生效的上限变更，按 (at, seq) 排序
	nextSeq  int64
	logger   Logger
}

// NewStation 创建充电站。totalCap 为初始总功率上限（正整数），
// ports 不可为空且每个接口上限必须为正；接口 ID 不可重复、不能为空。
func NewStation(totalCap int, ports []Port) (*Station, error) {
	if totalCap <= 0 || len(ports) == 0 {
		return nil, ErrInvalidArg
	}
	pm := make(map[string]*Port, len(ports))
	for _, p := range ports {
		if p.ID == "" || p.Cap <= 0 {
			return nil, ErrInvalidArg
		}
		if _, dup := pm[p.ID]; dup {
			return nil, ErrInvalidArg
		}
		p := p
		pm[p.ID] = &p
	}
	return &Station{
		now:      0,
		totalCap: totalCap,
		ports:    pm,
		portSess: make(map[string]*Session),
		sess:     make(map[int64]*Session),
	}, nil
}

// SetLogger 安装操作日志器（记录输入、输出与判定依据）。
func (st *Station) SetLogger(l Logger) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.logger = l
}

// PlugRequest 为插枪参数。
type PlugRequest struct {
	At     int64    // 事件时刻（不得早于当前时钟）
	PortID string   // 接口 ID
	Need   int      // 需求电量（正整数）
	MaxP   int      // 车辆最大功率（正整数）
	MinP   int      // 最低可用功率（0..MaxP）
	Prio   Priority // 优先级类别
}

// Plug 插枪创建会话并触发重新分配，返回会话 ID。
func (st *Station) Plug(req PlugRequest) (int64, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if req.At < 0 || req.Need <= 0 || req.MaxP <= 0 || req.MinP < 0 || req.MinP > req.MaxP ||
		(req.Prio != Normal && req.Prio != Prefer) {
		return 0, st.fail(fmt.Sprintf("插枪 参数非法 %+v", req), ErrInvalidArg)
	}
	if req.At < st.now {
		return 0, st.fail(fmt.Sprintf("插枪 时钟回退 at=%d now=%d", req.At, st.now), ErrClockBack)
	}
	port, ok := st.ports[req.PortID]
	if !ok {
		return 0, st.fail(fmt.Sprintf("插枪 接口不存在 port=%s", req.PortID), ErrPortMissing)
	}
	if _, busy := st.portSess[req.PortID]; busy {
		return 0, st.fail(fmt.Sprintf("插枪 接口占用 port=%s", req.PortID), ErrPortOccupied)
	}

	st.nextID++
	newID := st.nextID
	st.advanceLocked(req.At)
	s := &Session{
		ID:       newID,
		PortID:   req.PortID,
		PlugAt:   req.At,
		Need:     req.Need,
		MaxP:     req.MaxP,
		MinP:     req.MinP,
		Prio:     req.Prio,
		State:    StateWaiting,
		UnplugAt: -1,
		FullAt:   -1,
		selfCap:  minInt(req.MaxP, port.Cap),
		active:   true,
	}
	st.sess[s.ID] = s
	st.portSess[req.PortID] = s
	st.reallocateLocked()
	st.logf("插枪 ok id=%d port=%s at=%d need=%d max=%d min=%d prio=%d -> 状态=%s 功率=%d",
		s.ID, req.PortID, req.At, req.Need, req.MaxP, req.MinP, req.Prio,
		s.State, s.Power)
	return newID, nil
}

// Unplug 拔枪结束会话，按拒绝次序校验后结算到事件时刻，返回已充电量。
func (st *Station) Unplug(sessionID int64, at int64) (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if at < 0 {
		return 0, st.fail(fmt.Sprintf("拔枪 参数非法 id=%d at=%d", sessionID, at), ErrInvalidArg)
	}
	if at < st.now {
		return 0, st.fail(fmt.Sprintf("拔枪 时钟回退 id=%d at=%d now=%d", sessionID, at, st.now), ErrClockBack)
	}
	s, ok := st.sess[sessionID]
	if !ok {
		return 0, st.fail(fmt.Sprintf("拔枪 会话不存在 id=%d", sessionID), ErrNoSession)
	}

	st.advanceLocked(at)

	// advanceLocked 不会删除会话；拔枪前再确认仍在站。
	if !s.active {
		return 0, st.fail(fmt.Sprintf("拔枪 会话不存在 id=%d", sessionID), ErrNoSession)
	}
	charged := s.Charged
	s.State = StateEnded
	s.Power = 0
	s.UnplugAt = at
	s.active = false
	delete(st.portSess, s.PortID)
	st.reallocateLocked()
	st.logf("拔枪 ok id=%d at=%d 已充电量=%d", sessionID, at, charged)
	return charged, nil
}

// SetCap 在 at 时刻将总功率上限改为 cap（正整数）。
// at 不得早于当前时刻；变更使当前分配超限或在当前时刻生效时立即重分。
func (st *Station) SetCap(at int64, cap int) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if at < 0 || cap <= 0 {
		return st.fail(fmt.Sprintf("上限变更 参数非法 at=%d cap=%d", at, cap), ErrInvalidArg)
	}
	if at < st.now {
		return st.fail(fmt.Sprintf("上限变更 时钟回退 at=%d now=%d", at, st.now), ErrClockBack)
	}

	if at == st.now {
		old := st.totalCap
		st.totalCap = cap
		over := st.powerSumLocked() > cap
		if st.applyChangesLocked() {
			// 同时刻更早登记的变更已按序生效，最终值以本次调用为准。
			st.totalCap = cap
		}
		st.reallocateLocked()
		st.logf("上限变更 即时 old=%d new=%d at=%d 超限=%v", old, cap, at, over)
		return nil
	}

	st.nextSeq++
	st.changes = append(st.changes, capChange{at: at, cap: cap, seq: st.nextSeq})
	sort.SliceStable(st.changes, func(i, j int) bool {
		if st.changes[i].at != st.changes[j].at {
			return st.changes[i].at < st.changes[j].at
		}
		return st.changes[i].seq < st.changes[j].seq
	})
	st.logf("上限变更 预约 at=%d cap=%d", at, cap)
	return nil
}

// SetPriority 变更在站车辆的优先级类别。已充满/已结束的会话报状态不允许。
// 插枪先后序不变；事件后按新类别参与下一次分配。
func (st *Station) SetPriority(sessionID int64, at int64, prio Priority) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if at < 0 || (prio != Normal && prio != Prefer) {
		return st.fail(fmt.Sprintf("优先级变更 参数非法 id=%d at=%d prio=%d", sessionID, at, prio), ErrInvalidArg)
	}
	if at < st.now {
		return st.fail(fmt.Sprintf("优先级变更 时钟回退 id=%d at=%d now=%d", sessionID, at, st.now), ErrClockBack)
	}
	s, ok := st.sess[sessionID]
	if !ok {
		return st.fail(fmt.Sprintf("优先级变更 会话不存在 id=%d", sessionID), ErrNoSession)
	}
	if s.State == StateFull || s.State == StateEnded {
		return st.fail(fmt.Sprintf("优先级变更 状态不允许 id=%d 状态=%s", sessionID, s.State), ErrWrongState)
	}

	st.advanceLocked(at)

	if s.State == StateFull || s.State == StateEnded {
		return st.fail(fmt.Sprintf("优先级变更 状态不允许 id=%d 状态=%s", sessionID, s.State), ErrWrongState)
	}
	old := s.Prio
	s.Prio = prio
	st.reallocateLocked()
	st.logf("优先级变更 ok id=%d %d->%d at=%d -> 状态=%s 功率=%d",
		sessionID, old, prio, at, s.State, s.Power)
	return nil
}

// AdvanceTo 显式推进时钟。返回推进区间内发生的充满事件（按时刻、插枪序）。
func (st *Station) AdvanceTo(at int64) ([]FillEvent, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if at < 0 {
		return nil, st.fail(fmt.Sprintf("推进 参数非法 at=%d", at), ErrInvalidArg)
	}
	if at < st.now {
		return nil, st.fail(fmt.Sprintf("推进 时钟回退 at=%d now=%d", at, st.now), ErrClockBack)
	}

	events := st.advanceLocked(at)
	st.reallocateLocked()
	st.logf("推进 ok now=%d 充满=%d", st.now, len(events))
	return events, nil
}

// reallocateLocked 取在站车辆快照并执行全量重分。
func (st *Station) reallocateLocked() {
	list := st.activeOrderedLocked()
	allocate(list, st.totalCap)
}

func (st *Station) powerSumLocked() int {
	t := 0
	for _, s := range st.portSess {
		t += s.Power
	}
	return t
}

func (st *Station) logf(format string, args ...any) {
	if st.logger != nil {
		st.logger.Log(fmt.Sprintf(format, args...))
	}
}

func (st *Station) fail(line string, err error) error {
	st.logf("%s -> 拒绝: %v", line, err)
	return err
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
