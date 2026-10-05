// Package ward 维护病区、房间与床位的状态模型。
//
// 清洁中与预留的「到期」不通过任何后台任务处理，而是 now 的纯函数：
// 清洁中的床在 now >= CleanAt 时视为空闲（恰等可用），预留在
// now >= ResAt+H 时视为不存在（恰等即失效，直接回到空闲）。
package ward

// Sex 患者性别，仅男或女。
type Sex string

const (
	Male   Sex = "M"
	Female Sex = "F"
)

// Valid 报告性别取值是否合法。
func (s Sex) Valid() bool { return s == Male || s == Female }

// BedStatus 床位的四种物理状态；对外判定一律使用 EffStatus。
type BedStatus int

const (
	Free BedStatus = iota
	Occupied
	Reserved
	Cleaning
)

// Bed 一张床。床号为其在 Room.Beds 中的下标加一。
type Bed struct {
	Status  BedStatus
	Patient string // 占用者或预留者；空闲/清洁中为空
	Sex     Sex    // 占用者或预留者的性别
	CleanAt int    // 清洁完成时刻（Cleaning 时有效）
	ResAt   int    // 预留建立时刻（Reserved 时有效）
}

// Room 一个房间，床号为 1..len(Beds)。
type Room struct {
	ID    string
	Beds  []Bed
	IsoBy string // 隔离标志：持有者的患者编号，空表示无标志
}

// Ward 一个病区，房号在病区内唯一。
type Ward struct {
	ID    string
	Rooms map[string]*Room
}

// NewWard 创建空病区。
func NewWard(id string) *Ward {
	return &Ward{ID: id, Rooms: make(map[string]*Room)}
}

// TotalBeds 病区床位总数。
func (w *Ward) TotalBeds() int {
	n := 0
	for _, r := range w.Rooms {
		n += len(r.Beds)
	}
	return n
}

// EffStatus 床在 now 时刻的有效状态，now 的纯函数。
// h 为预留有效期。清洁恰等到期即可用，预留恰等到期即失效。
func (b *Bed) EffStatus(now, h int) BedStatus {
	switch b.Status {
	case Cleaning:
		if now >= b.CleanAt {
			return Free
		}
	case Reserved:
		if now >= b.ResAt+h {
			return Free
		}
	}
	return b.Status
}

// Occupants 房间在 now 时刻的在房者（占用者与有效预留者）。
func (r *Room) Occupants(now, h int) []string {
	var ps []string
	for i := range r.Beds {
		b := &r.Beds[i]
		switch b.EffStatus(now, h) {
		case Occupied, Reserved:
			ps = append(ps, b.Patient)
		}
	}
	return ps
}

// IsoActive 隔离标志在 now 时刻是否有效：标志持有者仍占用或有效预留本房间的床。
func (r *Room) IsoActive(now, h int) bool {
	if r.IsoBy == "" {
		return false
	}
	for i := range r.Beds {
		b := &r.Beds[i]
		if b.Patient != r.IsoBy {
			continue
		}
		switch b.EffStatus(now, h) {
		case Occupied, Reserved:
			return true
		}
	}
	return false
}
