// Package room 提供多房间的组队入座与席位预留管理器。
//
// 并发：所有操作由单互斥锁串行化，结果等价于某个串行顺序。
// 到期：采用惰性视角 —— 判定阶段把 expiry <= now 的预留视为已释放，
// 仅在操作被接受时通过全局最小堆真正落地，因此被拒操作零副作用。
package room

import (
	"errors"
	"sync"

	"ontology/party"
	"ontology/seat"
)

// State 是席位状态，取自 seat 包。
type State = seat.State

const (
	Empty    = seat.Empty
	Reserved = seat.Reserved
	Seated   = seat.Seated
)

// 参数边界。
const (
	minTeams = 2
	maxTeams = 8
	minSeats = 1
	maxSeats = 16
	maxHold  = int64(1_000_000_000)
	maxNow   = int64(1_000_000_000_000)
)

// 拒绝原因。各操作的判定次序见 DESIGN.md 与题面。
var (
	ErrInvalidParam  = errors.New("参数非法")
	ErrClockSkew     = errors.New("时钟回退")
	ErrRoomExists    = errors.New("房间已存在")
	ErrRoomNotFound  = errors.New("房间不存在")
	ErrPartyExists   = errors.New("组队已存在")
	ErrPartyNotFound = errors.New("组队不存在")
	ErrMemberBound   = errors.New("成员已属其他组队")
	ErrBusy          = errors.New("成员已持有席位或有效预留")
	ErrBanned        = errors.New("成员被本房间禁入")
	ErrNoSeat        = errors.New("空席不足")
	ErrImbalance     = errors.New("队伍失衡")
	ErrNoReservation = errors.New("无有效预留")
	ErrNotInRoom     = errors.New("不在房间")
	ErrNotOwner      = errors.New("不是房主")
)

// SeatView 是单个席位的快照。
type SeatView struct {
	Team     int
	Index    int
	Occupant string
	State    State
}

// RoomView 是房间快照：全部席位（队号、位号升序）与当前房主。
type RoomView struct {
	Owner string
	Seats []SeatView
}

// reservation 是一组共用一个到期时刻的预留记录。
type reservation struct {
	rid       string
	expiry    int64
	seq       int64
	members   []string
	remaining int // 仍挂在该记录上的（未确认、未离开）成员数
	heapIdx   int
}

// holding 是玩家当前持有的席位或预留。
type holding struct {
	rid      string
	reserved bool // true=预留中，false=已入座
	team     int
	idx      int
	rec      *reservation // 仅预留中有效
}

// active 报告持有在 now 视角下是否有效：入座恒有效，预留需未到期。
func (h *holding) active(now int64) bool {
	if h == nil {
		return false
	}
	if !h.reserved {
		return true
	}
	return h.rec.expiry > now
}

// Room 是单个房间。
type Room struct {
	rid         string
	d           int
	h           int64
	table       *seat.Table
	banned      map[string]bool
	nextConfirm int64
	owner       string
	seated      map[string]int64 // 在座玩家 -> 确认序号
}

// Manager 管理全部房间、组队与玩家持有，是并发安全的。
type Manager struct {
	mu       sync.Mutex
	rooms    map[string]*Room
	parties  *party.Registry
	holds    map[string]*holding
	exp      expiryHeap
	seq      int64
	lastNow  int64
	clockSet bool
	touched  int // 本次操作为落地到期检查的预留记录数
}

// NewManager 创建空管理器。
func NewManager() *Manager {
	return &Manager{
		rooms:   make(map[string]*Room),
		parties: party.NewRegistry(),
		holds:   make(map[string]*holding),
	}
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// checkClock 校验全局时钟单调性。
func (m *Manager) checkClock(now int64) error {
	if m.clockSet && now < m.lastNow {
		return ErrClockSkew
	}
	return nil
}

// commit 在接受操作时调用：先落地全部到期预留，再推进时钟。
func (m *Manager) commit(now int64) {
	m.land(now)
	m.lastNow = now
	m.clockSet = true
}

// land 弹出并释放全部 expiry <= now 的预留记录。
// 检查记录数 = 释放组数 + 1（最后一次 peek 发现堆顶未到期）。
func (m *Manager) land(now int64) {
	for len(m.exp) > 0 {
		m.touched++
		top := m.exp[0]
		if top.expiry > now {
			return
		}
		m.exp.pop()
		m.release(top)
	}
}

// release 释放一条到期预留：仅释放同组中尚未确认的成员，已确认者留下。
func (m *Manager) release(rec *reservation) {
	rm := m.rooms[rec.rid]
	for _, name := range rec.members {
		h := m.holds[name]
		if h != nil && h.reserved && h.rec == rec {
			rm.table.Clear(h.team, h.idx)
			delete(m.holds, name)
		}
	}
}

// releaseHold 释放玩家当前的席位或有效预留（Leave/Kick 用）。
func (m *Manager) releaseHold(rm *Room, player string, h *holding) {
	rm.table.Clear(h.team, h.idx)
	delete(m.holds, player)
	if h.reserved {
		rec := h.rec
		rec.remaining--
		if rec.remaining == 0 {
			m.exp.remove(rec)
		}
	} else {
		delete(rm.seated, player)
	}
}

// ownerOf 返回在座玩家中确认序号最小者，无人在座时为空串。
func ownerOf(rm *Room) string {
	best := ""
	var bestSeq int64
	for p, s := range rm.seated {
		if best == "" || s < bestSeq {
			best, bestSeq = p, s
		}
	}
	return best
}

// NewRoom 创建房间。rid 重复报 ErrRoomExists。
func (m *Manager) NewRoom(rid string, t, s, d int, h int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touched = 0
	if rid == "" || t < minTeams || t > maxTeams ||
		s < minSeats || s > maxSeats || d < 1 || d > s ||
		h < 1 || h > maxHold {
		return ErrInvalidParam
	}
	if _, ok := m.rooms[rid]; ok {
		return ErrRoomExists
	}
	m.rooms[rid] = &Room{
		rid:    rid,
		d:      d,
		h:      h,
		table:  seat.NewTable(t, s),
		banned: make(map[string]bool),
		seated: make(map[string]int64),
	}
	return nil
}

// FormParty 登记组队。拒绝次序：参数非法 > pid 已存在 > 成员已属其他组队。
func (m *Manager) FormParty(pid string, members []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touched = 0
	if pid == "" || !party.ValidMembers(members) {
		return ErrInvalidParam
	}
	if m.parties.Exists(pid) {
		return ErrPartyExists
	}
	for _, name := range members {
		if m.parties.Bound(name) {
			return ErrMemberBound
		}
	}
	m.parties.Add(pid, members)
	return nil
}

// Reserve 整组预留同一队的连续空位（按位号升序分给登记次序的成员），
// 全有或全无，全组共用一个到期时刻 now+H。
func (m *Manager) Reserve(now int64, rid, pid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touched = 0
	if !validNow(now) || rid == "" || pid == "" {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	rm, ok := m.rooms[rid]
	if !ok {
		return ErrRoomNotFound
	}
	p := m.parties.Get(pid)
	if p == nil {
		return ErrPartyNotFound
	}
	for _, name := range p.Members {
		if m.holds[name].active(now) {
			return ErrBusy
		}
	}
	for _, name := range p.Members {
		if rm.banned[name] {
			return ErrBanned
		}
	}
	occ := rm.table.Occupancies(now)
	team := rm.table.PickTeam(now)
	free := rm.table.FreeSeats(team, now)
	n := len(p.Members)
	if len(free) < n {
		return ErrNoSeat
	}
	lo, hi := int(^uint(0)>>1), 0
	for i, o := range occ {
		if i == team {
			o += n
		}
		if o < lo {
			lo = o
		}
		if o > hi {
			hi = o
		}
	}
	if hi-lo > rm.d {
		return ErrImbalance
	}
	m.commit(now)
	m.seq++
	rec := &reservation{
		rid:       rid,
		expiry:    now + rm.h,
		seq:       m.seq,
		members:   p.Members,
		remaining: n,
	}
	for i, name := range p.Members {
		idx := free[i]
		rm.table.Assign(team, idx, name, seat.Reserved, rec.expiry)
		m.holds[name] = &holding{rid: rid, reserved: true, team: team, idx: idx, rec: rec}
	}
	m.exp.push(rec)
	return nil
}

// Confirm 把玩家的有效预留转为入座，分配从 1 递增的确认序号。
func (m *Manager) Confirm(now int64, rid, player string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touched = 0
	if !validNow(now) || rid == "" || player == "" {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	rm, ok := m.rooms[rid]
	if !ok {
		return ErrRoomNotFound
	}
	h := m.holds[player]
	if h == nil || h.rid != rid || !h.reserved || h.rec.expiry <= now {
		return ErrNoReservation
	}
	m.commit(now)
	rec := h.rec
	rec.remaining--
	if rec.remaining == 0 {
		m.exp.remove(rec)
	}
	rm.table.Assign(h.team, h.idx, player, seat.Seated, 0)
	h.reserved = false
	h.rec = nil
	rm.nextConfirm++
	rm.seated[player] = rm.nextConfirm
	if rm.owner == "" {
		rm.owner = player
	}
	return nil
}

// Leave 释放玩家的席位或有效预留；房主离开时按确认序号移交。
func (m *Manager) Leave(now int64, rid, player string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touched = 0
	if !validNow(now) || rid == "" || player == "" {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	rm, ok := m.rooms[rid]
	if !ok {
		return ErrRoomNotFound
	}
	h := m.holds[player]
	if h == nil || h.rid != rid || !h.active(now) {
		return ErrNotInRoom
	}
	m.commit(now)
	m.releaseHold(rm, player, h)
	if rm.owner == player {
		rm.owner = ownerOf(rm)
	}
	return nil
}

// Kick 由房主踢出目标：释放其席位或有效预留，并永久禁入本房间。
func (m *Manager) Kick(now int64, rid, by, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touched = 0
	if !validNow(now) || rid == "" || by == "" || target == "" || by == target {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	rm, ok := m.rooms[rid]
	if !ok {
		return ErrRoomNotFound
	}
	if rm.owner == "" || rm.owner != by {
		return ErrNotOwner
	}
	h := m.holds[target]
	if h == nil || h.rid != rid || !h.active(now) {
		return ErrNotInRoom
	}
	m.commit(now)
	m.releaseHold(rm, target, h)
	rm.banned[target] = true
	return nil
}

// Seats 返回 now 视角下各席位的占用者与状态及房主。
func (m *Manager) Seats(now int64, rid string) (RoomView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touched = 0
	if !validNow(now) || rid == "" {
		return RoomView{}, ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return RoomView{}, err
	}
	rm, ok := m.rooms[rid]
	if !ok {
		return RoomView{}, ErrRoomNotFound
	}
	m.commit(now)
	view := RoomView{Owner: rm.owner}
	for team := 0; team < rm.table.Teams(); team++ {
		for idx := 0; idx < rm.table.Size(); idx++ {
			c := rm.table.Cell(team, idx)
			view.Seats = append(view.Seats, SeatView{
				Team:     team,
				Index:    idx,
				Occupant: c.Occupant,
				State:    c.State,
			})
		}
	}
	return view, nil
}
