package station

import (
	"sort"
	"sync"
)

// sess 是存活会话的内部表示。
type sess struct {
	Session
	remaining int // 需求剩余电量
}

// Station 是充电站控制器。
type Station struct {
	mu       sync.Mutex
	now      int
	cap      int
	ports    map[string]*Port
	portSess map[string]string // portID -> sessionID
	sessions map[string]*sess
	plugSeq  int64
	idSeq    int64
	pending  []capChange // 按生效时刻有序的未来上限变更
}

type capChange struct {
	at  int
	cap int
	seq int64
}

// New 创建充电站；cap 为初始总功率上限，ports 描述各接口。
func New(cap int, ports []Port) *Station {
	if cap < 0 {
		cap = 0
	}
	st := &Station{
		now:      0,
		cap:      cap,
		ports:    make(map[string]*Port, len(ports)),
		portSess: make(map[string]string, len(ports)),
		sessions: make(map[string]*sess),
	}
	for i := range ports {
		p := ports[i]
		if p.MaxPwr < 0 {
			continue
		}
		if _, dup := st.ports[p.ID]; dup {
			continue
		}
		st.ports[p.ID] = &p
	}
	return st
}

// Plug 插枪创建会话。
func (s *Station) Plug(p PlugParams) (string, error) {
	if p.Demand <= 0 || p.CarMax <= 0 || p.MinPwr < 0 || p.MinPwr > p.CarMax ||
		(p.Priority != PriorityNormal && p.Priority != PriorityFast) {
		return "", ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ports[p.PortID]; !ok {
		return "", ErrPortMissing
	}
	if _, busy := s.portSess[p.PortID]; busy {
		return "", ErrPortBusy
	}
	id := p.SessionID
	if id == "" {
		s.idSeq++
		id = "s" + itoa(s.idSeq)
	} else if _, exists := s.sessions[id]; exists {
		return "", ErrInvalidParam
	}
	s.plugSeq++
	se := &sess{
		Session: Session{
			ID:        id,
			PortID:    p.PortID,
			PlugOrder: s.plugSeq,
			Priority:  p.Priority,
			State:     StateWaiting,
			Demand:    p.Demand,
			MinPwr:    p.MinPwr,
			CarMax:    p.CarMax,
			Cap:       minInt(p.CarMax, s.ports[p.PortID].MaxPwr),
		},
		remaining: p.Demand,
	}
	s.sessions[id] = se
	s.portSess[p.PortID] = id
	s.reallocate(causeOther)
	return id, nil
}

// Unplug 拔枪结束会话，返回已充电量。
func (s *Station) Unplug(sessionID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	se, ok := s.sessions[sessionID]
	if !ok {
		return 0, ErrSessionGone
	}
	energy := se.Energy
	delete(s.sessions, sessionID)
	delete(s.portSess, se.PortID)
	s.reallocate(causeOther)
	return energy, nil
}

// SetPriority 调整在站车辆的优先级类别。
func (s *Station) SetPriority(sessionID string, p Priority) error {
	if p != PriorityNormal && p != PriorityFast {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	se, ok := s.sessions[sessionID]
	if !ok {
		return ErrSessionGone
	}
	if se.State == StateFull {
		return ErrState
	}
	if se.Priority == p {
		return nil
	}
	demoted := map[string]bool{}
	if se.Priority == PriorityFast && p == PriorityNormal {
		demoted[se.ID] = true
	}
	se.Priority = p
	s.reallocateCtx(reallocCtx{cause: causeOther, demoted: demoted})
	return nil
}

// ChangeCap 在 at 时刻生效新的总功率上限。
func (s *Station) ChangeCap(at, newCap int) error {
	if newCap < 0 || at < 0 {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at < s.now {
		return ErrClockBack
	}
	if at == s.now {
		s.cap = newCap
		s.reallocate(causeCapDown)
		return nil
	}
	s.idSeq++
	ch := capChange{at: at, cap: newCap, seq: s.idSeq}
	pos := 0
	for pos < len(s.pending) && (s.pending[pos].at < at ||
		(s.pending[pos].at == at && s.pending[pos].seq < ch.seq)) {
		pos++
	}
	s.pending = append(s.pending, capChange{})
	copy(s.pending[pos+1:], s.pending[pos:])
	s.pending[pos] = ch
	return nil
}

// Advance 显式推进时钟到 t。
func (s *Station) Advance(t int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.advance(t)
}

// Now 返回当前时刻。
func (s *Station) Now() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Snapshot 返回当前完整状态副本。
func (s *Station) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{Now: s.now, Cap: s.cap}
	for _, p := range s.ports {
		pv := PortView{ID: p.ID, MaxPwr: p.MaxPwr, SessionID: s.portSess[p.ID]}
		snap.Ports = append(snap.Ports, pv)
	}
	for _, se := range s.sessions {
		snap.Sessions = append(snap.Sessions, se.Session)
	}
	sortViews(snap)
	return snap
}

// PowerOf 返回某会话当前分配功率。
func (s *Station) PowerOf(sessionID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	se, ok := s.sessions[sessionID]
	if !ok {
		return 0, ErrSessionGone
	}
	return se.Pwr, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func sortViews(snap Snapshot) {
	sort.Slice(snap.Ports, func(i, j int) bool { return snap.Ports[i].ID < snap.Ports[j].ID })
	sort.Slice(snap.Sessions, func(i, j int) bool {
		return snap.Sessions[i].PlugOrder < snap.Sessions[j].PlugOrder
	})
}
