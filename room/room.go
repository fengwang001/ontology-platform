// Package room 实现多房间的整组预留、确认入座、房主与禁入管理。
package room

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/party"
	"ontology/seat"
)

var (
	// ErrInvalidParam 参数非法。
	ErrInvalidParam = party.ErrInvalidParam
	// ErrClockRollback now 小于已接受操作的最大 now。
	ErrClockRollback = errors.New("room: clock rollback")
	// ErrRoomExists 房间 id 已存在。
	ErrRoomExists = errors.New("room: room exists")
	// ErrRoomNotFound 房间不存在。
	ErrRoomNotFound = errors.New("room: room not found")
	// ErrPartyExists 组队已存在。
	ErrPartyExists = party.ErrPartyExists
	// ErrPartyNotFound 组队不存在。
	ErrPartyNotFound = errors.New("room: party not found")
	// ErrMemberBusy 成员已属于其他组队。
	ErrMemberBusy = party.ErrMemberBusy
	// ErrOccupied 组内有成员在任一房间持有席位或有效预留。
	ErrOccupied = errors.New("room: member occupied elsewhere")
	// ErrBanned 组内有成员被本房间禁入。
	ErrBanned = errors.New("room: member banned")
	// ErrNoSeats 选中队空席不足。
	ErrNoSeats = errors.New("room: not enough free seats")
	// ErrImbalance 入座后失衡超过上限。
	ErrImbalance = errors.New("room: team imbalance")
	// ErrNoReservation 玩家在本房间没有有效预留。
	ErrNoReservation = errors.New("room: no valid reservation")
	// ErrNotInRoom 玩家在本房间既无席位也无有效预留。
	ErrNotInRoom = errors.New("room: not in room")
	// ErrNotHost by 不是房主。
	ErrNotHost = errors.New("room: not the host")
)

// View 为 Seats 返回的房间视图（到期视角之后）。
type View struct {
	Cells []seat.Cell
	Host  string
}

// hold 是一支组队在本房间的整组预留记录；部分成员确认后记录继续保留直至全组离开。
type hold struct {
	pid    string
	team   int
	expire int64
	// members[i] 为成员名，seats[i] 为其席号；confirmed[i] 标记是否已确认在座。
	members   []string
	seats     []int
	confirmed []bool
	index     int // 在到期堆中的下标
}

// expireHeap 为按到期时刻排序的最小堆，队号/组队 id 仅用于稳定次序。
type expireHeap []*hold

func (h expireHeap) Len() int { return len(h) }
func (h expireHeap) Less(i, j int) bool {
	if h[i].expire != h[j].expire {
		return h[i].expire < h[j].expire
	}
	if h[i].team != h[j].team {
		return h[i].team < h[j].team
	}
	return h[i].pid < h[j].pid
}
func (h expireHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *expireHeap) Push(x any) {
	hd := x.(*hold)
	hd.index = len(*h)
	*h = append(*h, hd)
}
func (h *expireHeap) Pop() any {
	old := *h
	n := len(old)
	hd := old[n-1]
	old[n-1] = nil
	hd.index = -1
	*h = old[:n-1]
	return hd
}

type roomState struct {
	teams  int
	maxD   int
	holdMs int64
	table  *seat.Table

	byParty  map[string]*hold
	byPlayer map[string]*hold
	heap     expireHeap
	banned   map[string]struct{}

	confirmSeq int
	seqOf      map[string]int
	host       string
}

// loc 为某玩家当前在全局的定位（无玩家在多于一处持有席位/有效预留）。
type loc struct {
	rid        string
	h          *hold
	pos        int
	standalone bool // 预留已到期且本人已确认：座位独立保留
	team       int
	seatIdx    int
}

// Manager 管理全部房间、组队与全局玩家定位。
type Manager struct {
	mu      sync.Mutex
	rooms   map[string]*roomState
	book    *party.Book
	players map[string]*loc
	maxNow  int64
	touched int
}

// NewManager 创建空管理器。
func NewManager() *Manager {
	return &Manager{
		rooms:   map[string]*roomState{},
		book:    party.NewBook(),
		players: map[string]*loc{},
	}
}

// NewRoom 登记房间。T∈[2,8]，S∈[1,16]，D∈[1,S]，H∈[1,1e9]。
func (m *Manager) NewRoom(rid string, teams, seatsPerTeam, imbalance int, holdMillis int64) error {
	if rid == "" || teams < 2 || teams > 8 || seatsPerTeam < 1 || seatsPerTeam > 16 ||
		imbalance < 1 || imbalance > seatsPerTeam || holdMillis < 1 || holdMillis > 1_000_000_000 {
		return ErrInvalidParam
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.rooms[rid]; exists {
		return ErrRoomExists
	}
	r := &roomState{
		teams:    teams,
		maxD:     imbalance,
		holdMs:   holdMillis,
		table:    seat.NewTable(teams, seatsPerTeam),
		byParty:  map[string]*hold{},
		byPlayer: map[string]*hold{},
		banned:   map[string]struct{}{},
		seqOf:    map[string]int{},
	}
	r.heap = expireHeap{}
	heap.Init(&r.heap)
	m.rooms[rid] = r
	return nil
}

// FormParty 登记组队，委托 party.Book。
func (m *Manager) FormParty(pid string, members []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.book.Form(pid, members)
	return err
}

func (m *Manager) checkNow(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	if now < m.maxNow {
		return ErrClockRollback
	}
	return nil
}

// freeInTeam 只读视角下某队的空席：席位为空且不被有效预留占用，按编号升序。
func (r *roomState) freeInTeam(team int, now int64) []int {
	reserved := map[int]bool{}
	for _, h := range r.byParty {
		if h.expire <= now || h.team != team {
			continue
		}
		for i, p := range h.members {
			if p != "" && !h.confirmed[i] {
				reserved[h.seats[i]] = true
			}
		}
	}
	out := []int{}
	for _, idx := range r.table.FreeIndices(team) {
		if !reserved[idx] {
			out = append(out, idx)
		}
	}
	return out
}

// occupancy 只读视角下各队占用数（在座人数 + 有效预留人数）。
func (r *roomState) occupancy(now int64) []int {
	count := make([]int, r.teams)
	for _, c := range r.table.Snapshot() {
		if c.Status == seat.Seated {
			count[c.Team]++
		}
	}
	for _, h := range r.byParty {
		if h.expire <= now {
			continue
		}
		for i, p := range h.members {
			if p != "" && !h.confirmed[i] {
				count[h.team]++
			}
		}
	}
	return count
}

// materializeHold 处理单个已到期 hold：释放未确认成员，已确认者座位转为独立在座。
func (m *Manager) materializeHold(r *roomState, hd *hold) {
	if hd.index >= 0 {
		heap.Remove(&r.heap, hd.index)
	}
	for i, p := range hd.members {
		if p == "" {
			continue
		}
		if hd.confirmed[i] {
			if lc, ok := m.players[p]; ok {
				lc.standalone = true
				lc.team = hd.team
				lc.seatIdx = hd.seats[i]
				lc.h = nil
			}
			delete(r.byPlayer, p)
			hd.members[i] = ""
			continue
		}
		r.table.Release(hd.team, hd.seats[i])
		delete(r.byPlayer, p)
		delete(m.players, p)
	}
	delete(r.byParty, hd.pid)
}

// expireAllLocked 为 Seats 物化本房所有 expire<=now 的预留，只释放其中未确认成员。
// touched 每检查一条堆顶记录加一；释放 k 组后再看一次堆顶（或空堆），总计 k+1。
func (m *Manager) expireAllLocked(r *roomState, now int64) {
	for {
		m.touched++
		if r.heap.Len() == 0 {
			return
		}
		top := r.heap[0]
		if top.expire > now {
			return
		}
		m.materializeHold(r, top)
	}
}

// removeMember 从 hold 中摘除单个成员（在座或预留）；全组离开后整体移除记录。
func (m *Manager) removeMember(r *roomState, h *hold, player string) {
	pos := -1
	for i, p := range h.members {
		if p == player {
			pos = i
			break
		}
	}
	if pos < 0 {
		return
	}
	r.table.Release(h.team, h.seats[pos])
	delete(r.byPlayer, player)
	delete(m.players, player)
	delete(r.seqOf, player)
	h.members[pos] = ""
	for _, p := range h.members {
		if p != "" {
			return
		}
	}
	if h.index >= 0 {
		heap.Remove(&r.heap, h.index)
	}
	delete(r.byParty, h.pid)
}

// recomputeHost 房主恒为在座者中确认序号最小者；无在座者则置空。
func (r *roomState) recomputeHost() {
	host := ""
	best := 0
	for _, c := range r.table.Snapshot() {
		if c.Status != seat.Seated {
			continue
		}
		sq, ok := r.seqOf[c.Player]
		if !ok {
			continue
		}
		if host == "" || sq < best {
			host, best = c.Player, sq
		}
	}
	r.host = host
}

// locActive 只读判断全局定位在 now 视角下是否仍为有效占用。
func locActive(l *loc, now int64) bool {
	if l.standalone {
		return true
	}
	pos := l.pos
	if l.h.members[pos] == "" {
		return false
	}
	return l.h.confirmed[pos] || l.h.expire > now
}

// presentInRoom 只读判断玩家此刻是否在指定房间持有席位或有效预留。
func presentInRoom(m *Manager, r *roomState, player string, now int64) bool {
	l, busy := m.players[player]
	if !busy {
		return false
	}
	if m.rooms[l.rid] != r {
		return false
	}
	if l.standalone {
		return true
	}
	h, pos := l.h, l.pos
	if h.members[pos] != player {
		return false
	}
	return h.confirmed[pos] || h.expire > now
}

// removeSeated 释放独立在座玩家（其预留已到期）。
func (m *Manager) removeSeated(r *roomState, l *loc, player string) {
	r.table.Release(l.team, l.seatIdx)
	delete(m.players, player)
	delete(r.seqOf, player)
}

// Reserve 以整组为单位预留同队席位，全有或全无。
func (m *Manager) Reserve(now int64, rid, pid string) error {
	if rid == "" || pid == "" || now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNow(now); err != nil {
		return err
	}
	r, ok := m.rooms[rid]
	if !ok {
		return ErrRoomNotFound
	}
	p, ok := m.book.Get(pid)
	if !ok {
		return ErrPartyNotFound
	}
	for _, member := range p.Members {
		if l, busy := m.players[member]; busy && locActive(l, now) {
			return ErrOccupied
		}
	}
	for _, member := range p.Members {
		if _, bad := r.banned[member]; bad {
			return ErrBanned
		}
	}
	count := r.occupancy(now)
	team := 0
	for tm := 1; tm < r.teams; tm++ {
		if count[tm] < count[team] {
			team = tm
		}
	}
	free := r.freeInTeam(team, now)
	if len(free) < len(p.Members) {
		return ErrNoSeats
	}
	lo, hi := count[team]+len(p.Members), count[team]+len(p.Members)
	for tm := 0; tm < r.teams; tm++ {
		if tm == team {
			continue
		}
		if count[tm] < lo {
			lo = count[tm]
		}
		if count[tm] > hi {
			hi = count[tm]
		}
	}
	if hi-lo > r.maxD {
		return ErrImbalance
	}

	m.touched = 0
	// 成功落地：本房到期预留的未确认席位此刻释放（与本次入座同一原子状态）。
	m.expireAllLocked(r, now)
	// 成员可能在他房持有已到期的未确认预留：到期视角下此刻一并物化释放。
	for _, member := range p.Members {
		if l, busy := m.players[member]; busy && !l.standalone && !l.h.confirmed[l.pos] &&
			l.h.members[l.pos] == member && l.h.expire <= now {
			m.materializeHold(m.rooms[l.rid], l.h)
		}
	}
	h := &hold{
		pid:       pid,
		team:      team,
		expire:    now + r.holdMs,
		members:   make([]string, len(p.Members)),
		seats:     make([]int, len(p.Members)),
		confirmed: make([]bool, len(p.Members)),
		index:     -1,
	}
	copy(h.members, p.Members)
	for i, member := range p.Members {
		idx := free[i]
		h.seats[i] = idx
		r.table.Place(team, idx, member, seat.Reserved)
		r.byPlayer[member] = h
		m.players[member] = &loc{rid: rid, h: h, pos: i}
	}
	r.byParty[pid] = h
	heap.Push(&r.heap, h)
	m.maxNow = now
	return nil
}

// Confirm 把玩家的有效预留转为在座，返回本房间内递增的确认序号。
func (m *Manager) Confirm(now int64, rid, player string) (int, error) {
	if rid == "" || player == "" || now < 0 || now > 1_000_000_000_000 {
		return 0, ErrInvalidParam
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNow(now); err != nil {
		return 0, err
	}
	r, ok := m.rooms[rid]
	if !ok {
		return 0, ErrRoomNotFound
	}
	if !presentInRoom(m, r, player, now) {
		return 0, ErrNoReservation
	}
	l := m.players[player]
	if l.standalone || l.h.confirmed[l.pos] {
		return 0, ErrNoReservation
	}
	m.touched = 0
	h, ok := r.byPlayer[player]
	if !ok {
		return 0, ErrNoReservation
	}
	pos := -1
	for i, p := range h.members {
		if p == player {
			pos = i
			break
		}
	}
	if pos < 0 || h.confirmed[pos] {
		return 0, ErrNoReservation
	}
	h.confirmed[pos] = true
	r.table.SetStatus(h.team, h.seats[pos], seat.Seated)
	r.confirmSeq++
	r.seqOf[player] = r.confirmSeq
	l.standalone = false
	r.recomputeHost()
	m.maxNow = now
	return r.confirmSeq, nil
}

// Leave 释放玩家的席位或有效预留；房主离开时按确认序号移交。
func (m *Manager) Leave(now int64, rid, player string) error {
	if rid == "" || player == "" || now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNow(now); err != nil {
		return err
	}
	r, ok := m.rooms[rid]
	if !ok {
		return ErrRoomNotFound
	}
	if !presentInRoom(m, r, player, now) {
		return ErrNotInRoom
	}
	m.touched = 0
	if l, standalone := m.players[player]; standalone && l.standalone && m.rooms[l.rid] == r {
		m.removeSeated(r, l, player)
	} else {
		l := m.players[player]
		if l == nil || l.standalone {
			return ErrNotInRoom
		}
		if l.h.expire <= now {
			// 到期视角下离座：整组预留此刻物化，未确认同伴同步释放。
			m.materializeHold(r, l.h)
			l = m.players[player]
			if l == nil {
				return ErrNotInRoom
			}
			m.removeSeated(r, l, player)
			r.recomputeHost()
			m.maxNow = now
			return nil
		}
		m.removeMember(r, l.h, player)
	}
	r.recomputeHost()
	m.maxNow = now
	return nil
}

// Kick 房主踢出目标：释放席位/预留并永久禁入。
func (m *Manager) Kick(now int64, rid, by, target string) error {
	if rid == "" || by == "" || target == "" || by == target ||
		now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNow(now); err != nil {
		return err
	}
	r, ok := m.rooms[rid]
	if !ok {
		return ErrRoomNotFound
	}
	if r.host != by {
		return ErrNotHost
	}
	if !presentInRoom(m, r, target, now) {
		return ErrNotInRoom
	}
	m.touched = 0
	if l, has := m.players[target]; has && l.standalone && m.rooms[l.rid] == r {
		m.removeSeated(r, l, target)
	} else {
		l := m.players[target]
		if l == nil || l.standalone {
			return ErrNotInRoom
		}
		if l.h.expire <= now {
			m.materializeHold(r, l.h)
			l = m.players[target]
			if l == nil {
				return ErrNotInRoom
			}
			m.removeSeated(r, l, target)
			r.banned[target] = struct{}{}
			r.recomputeHost()
			m.maxNow = now
			return nil
		}
		m.removeMember(r, l.h, target)
	}
	r.banned[target] = struct{}{}
	r.recomputeHost()
	m.maxNow = now
	return nil
}

// Seats 返回到期视角之后的席位视图。
func (m *Manager) Seats(now int64, rid string) (*View, error) {
	if rid == "" || now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidParam
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNow(now); err != nil {
		return nil, err
	}
	r, ok := m.rooms[rid]
	if !ok {
		return nil, ErrRoomNotFound
	}
	m.touched = 0
	m.expireAllLocked(r, now)
	r.recomputeHost()
	m.maxNow = now
	return &View{Cells: r.table.Snapshot(), Host: r.host}, nil
}
