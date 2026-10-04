// Package ward 维护病区、房间、床位与患者的状态。
//
// 床位状态是查询时刻 now 的纯函数：不使用后台任务清理过期预留或清洁，
// 同一组操作在相同 now 下重放结果必然一致。
package ward

import (
	"errors"
	"sort"
	"sync"
)

// 各操作的拒绝原因。调用方按规定次序只返回第一个匹配的错误。
var (
	ErrInvalid  = errors.New("ward: invalid argument")
	ErrClock    = errors.New("ward: clock moved backwards")
	ErrNotFound = errors.New("ward: ward or patient not found")
	ErrNoGrant  = errors.New("ward: operator has no grant on ward")
	ErrState    = errors.New("ward: patient state does not permit this operation")
	ErrNoBed    = errors.New("ward: no available bed")
	ErrNotSole  = errors.New("ward: room is not occupied solely by the patient")
)

// Sex 为患者性别。
type Sex int

const (
	// SexMale 男。
	SexMale Sex = iota + 1
	// SexFemale 女。
	SexFemale
)

// BedState 是床位在某个 now 下的派生状态。
type BedState int

const (
	// BedFree 空闲。
	BedFree BedState = iota
	// BedOccupied 占用。
	BedOccupied
	// BedReserved 有效预留。
	BedReserved
	// BedCleaning 清洁中。
	BedCleaning
)

// Bed 是一张床位。
type Bed struct {
	No         int
	Occupant   string // 占用患者，空表示无人占用
	ReservedBy string // 预留患者，空表示无预留
	ReserveAt  int64  // 预留时刻
	ReserveSex Sex    // 预留患者性别快照（在房者性别判定用）
	ReserveIso bool   // 预留患者是否隔离（隔离标志派生用）
	CleanUntil int64  // 清洁完成时刻；占用/预留期间无意义
}

// Room 是一个房间。
type Room struct {
	Name string
	Beds map[int]*Bed
}

// Ward 是一个病区。
type Ward struct {
	Name  string
	Rooms map[string]*Room
}

// Patient 是一名在院患者。
type Patient struct {
	ID       string
	Sex      Sex
	Iso      bool
	Ward     string
	Room     string
	Bed      int
	Transfer *Transfer
}

// Transfer 是一条在途转科（两阶段提交中的记录）。
type Transfer struct {
	ToWard    string
	ToRoom    string
	ToBed     int
	CreatedAt int64
}

// Hospital 是全部病区的状态载体，供三个包共享。
type Hospital struct {
	mu       sync.Mutex
	Cl       int64
	H        int64
	Wards    map[string]*Ward
	Patients map[string]*Patient
	Grants   map[string]map[string]bool // ward -> operator -> true
	LastNow  int64
}

// New 以清洁时长 Cl 与预留有效期 H 创建医院，二者均须在 [1,10000]。
func New(Cl, H int) (*Hospital, error) {
	if Cl < 1 || Cl > 10000 || H < 1 || H > 10000 {
		return nil, ErrInvalid
	}
	return &Hospital{
		Cl:       int64(Cl),
		H:        int64(H),
		Wards:    map[string]*Ward{},
		Patients: map[string]*Patient{},
		Grants:   map[string]map[string]bool{},
	}, nil
}

// Lock/Unlock 供 bedalloc、transfer 包在一次事务内持有医院锁。
func (h *Hospital) Lock()   { h.mu.Lock() }
func (h *Hospital) Unlock() { h.mu.Unlock() }

// AddRoom 在病区内新增房间；房号病区内唯一，床位数 1..8，床号 1..beds。
func (h *Hospital) AddRoom(wardName, room string, beds int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if wardName == "" || room == "" || beds < 1 || beds > 8 {
		return ErrInvalid
	}
	w := h.Wards[wardName]
	if w == nil {
		w = &Ward{Name: wardName, Rooms: map[string]*Room{}}
		h.Wards[wardName] = w
	} else if _, dup := w.Rooms[room]; dup {
		return ErrInvalid
	}
	r := &Room{Name: room, Beds: map[int]*Bed{}}
	for no := 1; no <= beds; no++ {
		r.Beds[no] = &Bed{No: no}
	}
	w.Rooms[room] = r
	return nil
}

// Grant 授予操作者对某病区的管理权（可重复授予）。
func (h *Hospital) Grant(op, wardName string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if op == "" || wardName == "" {
		return
	}
	g := h.Grants[wardName]
	if g == nil {
		g = map[string]bool{}
		h.Grants[wardName] = g
	}
	g[op] = true
}

// HasGrant 报告操作者是否管理该病区。
func (h *Hospital) HasGrant(op, wardName string) bool {
	return h.Grants[wardName][op]
}

// State 返回床位在 now 下的状态。
func (b *Bed) State(now, reserveTTL int64) BedState {
	if b.Occupant != "" {
		return BedOccupied
	}
	if b.ReservedBy != "" && now < b.ReserveAt+reserveTTL {
		return BedReserved
	}
	if now < b.CleanUntil {
		return BedCleaning
	}
	return BedFree
}

// TransferValid 报告转科预留是否在 now 仍有效（恰等即失效）。
func (p *Patient) TransferValid(now, reserveTTL int64) bool {
	t := p.Transfer
	return t != nil && now < t.CreatedAt+reserveTTL
}

// OrderedRooms 返回病区内按房号字节序排列的房间。
func (w *Ward) OrderedRooms() []*Room {
	out := make([]*Room, 0, len(w.Rooms))
	for _, r := range w.Rooms {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// OrderedBeds 返回房间内按床号升序排列的床位。
func (r *Room) OrderedBeds() []*Bed {
	out := make([]*Bed, 0, len(r.Beds))
	for _, b := range r.Beds {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].No < out[j].No })
	return out
}

// BedOf 定位患者占用床所在的床位对象；患者不在院或记录不存在返回 nil。
func (h *Hospital) BedOf(p *Patient) *Bed {
	w := h.Wards[p.Ward]
	if w == nil {
		return nil
	}
	r := w.Rooms[p.Room]
	if r == nil {
		return nil
	}
	return r.Beds[p.Bed]
}

// ReservedBed 定位患者在途转科的预留床位；无有效转科返回 nil（不判断时刻）。
func (h *Hospital) ReservedBed(p *Patient) *Bed {
	t := p.Transfer
	if t == nil {
		return nil
	}
	w := h.Wards[t.ToWard]
	if w == nil {
		return nil
	}
	r := w.Rooms[t.ToRoom]
	if r == nil {
		return nil
	}
	return r.Beds[t.ToBed]
}
