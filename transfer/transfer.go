// Package transfer 编排入院、出院、两阶段转科与隔离变更。
package transfer

import (
	"ontology/bedalloc"
	"ontology/ward"
)

// Service 是住院调配服务。
type Service struct {
	h       *ward.Hospital
	alloc   *bedalloc.Allocator
	touched int // 最近一次 Discharge/Confirm 触碰（改变）的不同床位数
}

// New 创建服务。
func New(h *ward.Hospital) *Service {
	return &Service{h: h, alloc: bedalloc.New(h)}
}

// Touched 返回最近一次 Discharge/Confirm 触碰的不同床位数。
func (s *Service) Touched() int {
	return s.touched
}

// Examined 返回最近一次 Admit/Request 的选床器考察床位数。
func (s *Service) Examined() int {
	return s.alloc.Examined()
}

// Admit 办理入院并占床。
func (s *Service) Admit(now int64, op, patient string, sex ward.Sex, iso bool, wardName string) (*bedalloc.Choice, error) {
	s.h.Lock()
	defer s.h.Unlock()
	if !validNow(now) || op == "" || patient == "" || (sex != ward.SexMale && sex != ward.SexFemale) || wardName == "" {
		return nil, ward.ErrInvalid
	}
	if now < s.h.LastNow {
		return nil, ward.ErrClock
	}
	w := s.h.Wards[wardName]
	if w == nil {
		return nil, ward.ErrNotFound
	}
	if !s.h.HasGrant(op, wardName) {
		return nil, ward.ErrNoGrant
	}
	if _, in := s.h.Patients[patient]; in {
		return nil, ward.ErrState
	}
	c := s.alloc.Select(now, w, sex, iso)
	if c == nil {
		return nil, ward.ErrNoBed
	}
	if old := c.Bed.ReservedBy; old != "" {
		// 该床的预留已到期：若旧患者尚未出院，其过期记录一并丢弃，
		// 否则之后对旧患者的操作会沿悬空指针碰到新入住者。
		if op := s.h.Patients[old]; op != nil {
			op.Transfer = nil
		}
	}
	clearReserve(c.Bed)
	c.Bed.Occupant = patient
	s.h.Patients[patient] = &ward.Patient{
		ID: patient, Sex: sex, Iso: iso,
		Ward: wardName, Room: c.Room.Name, Bed: c.Bed.No,
	}
	s.h.LastNow = now
	return c, nil
}

// Discharge 办理出院，床位进入清洁。
func (s *Service) Discharge(now int64, op, patient string) error {
	s.h.Lock()
	defer s.h.Unlock()
	s.touched = 0
	if !validNow(now) || op == "" || patient == "" {
		return ward.ErrInvalid
	}
	if now < s.h.LastNow {
		return ward.ErrClock
	}
	p := s.h.Patients[patient]
	if p == nil {
		return ward.ErrNotFound
	}
	if !s.h.HasGrant(op, p.Ward) {
		return ward.ErrNoGrant
	}
	// 隔离标志随患者离开而清除（标志由 iso 在房者派生）；
	// 在途转科预留一并取消。
	if p.Transfer != nil && p.TransferValid(now, s.h.H) {
		if rb := s.h.ReservedBed(p); rb != nil {
			clearReserve(rb)
			s.touched++
		}
	}
	p.Transfer = nil
	if b := s.h.BedOf(p); b != nil {
		b.Occupant = ""
		b.CleanUntil = now + s.h.Cl
		s.touched++
	}
	delete(s.h.Patients, patient)
	s.h.LastNow = now
	return nil
}

// Request 发起转科申请并在目标病区预留床位。
func (s *Service) Request(now int64, op, patient, toWard string) (*bedalloc.Choice, error) {
	s.h.Lock()
	defer s.h.Unlock()
	if !validNow(now) || op == "" || patient == "" || toWard == "" {
		return nil, ward.ErrInvalid
	}
	if now < s.h.LastNow {
		return nil, ward.ErrClock
	}
	p := s.h.Patients[patient]
	if p == nil {
		return nil, ward.ErrNotFound
	}
	target := s.h.Wards[toWard]
	if target == nil {
		return nil, ward.ErrNotFound
	}
	if toWard == p.Ward {
		return nil, ward.ErrInvalid
	}
	if !s.h.HasGrant(op, p.Ward) {
		return nil, ward.ErrNoGrant
	}
	if p.TransferValid(now, s.h.H) {
		return nil, ward.ErrState
	}
	c := s.alloc.Select(now, target, p.Sex, p.Iso)
	if c == nil {
		return nil, ward.ErrNoBed
	}
	c.Bed.ReservedBy = patient
	c.Bed.ReserveAt = now
	c.Bed.ReserveSex = p.Sex
	c.Bed.ReserveIso = p.Iso
	p.Transfer = &ward.Transfer{
		ToWard: toWard, ToRoom: c.Room.Name, ToBed: c.Bed.No, CreatedAt: now,
	}
	s.h.LastNow = now
	return c, nil
}

// Confirm 确认转科：患者迁入预留床，原床进入清洁。
func (s *Service) Confirm(now int64, op, patient string) error {
	s.h.Lock()
	defer s.h.Unlock()
	s.touched = 0
	if !validNow(now) || op == "" || patient == "" {
		return ward.ErrInvalid
	}
	if now < s.h.LastNow {
		return ward.ErrClock
	}
	p := s.h.Patients[patient]
	if p == nil {
		return ward.ErrNotFound
	}
	if !p.TransferValid(now, s.h.H) {
		return ward.ErrState
	}
	t := p.Transfer
	if !s.h.HasGrant(op, t.ToWard) {
		return ward.ErrNoGrant
	}
	oldBed := s.h.BedOf(p)
	newBed := s.h.ReservedBed(p)
	if newBed == nil {
		return ward.ErrState
	}
	// 迁入预留床：置占用后清除预留字段（该床未经过清洁）。
	newBed.Occupant = patient
	newBed.ReservedBy = ""
	newBed.ReserveAt = 0
	newBed.ReserveSex = 0
	newBed.ReserveIso = false
	s.touched++
	if oldBed != nil {
		oldBed.Occupant = ""
		oldBed.CleanUntil = now + s.h.Cl
		s.touched++
	}
	p.Ward = t.ToWard
	p.Room = t.ToRoom
	p.Bed = t.ToBed
	p.Transfer = nil
	s.h.LastNow = now
	return nil
}

// CancelTransfer 取消在途转科，释放预留。
func (s *Service) CancelTransfer(now int64, op, patient string) error {
	s.h.Lock()
	defer s.h.Unlock()
	if !validNow(now) || op == "" || patient == "" {
		return ward.ErrInvalid
	}
	if now < s.h.LastNow {
		return ward.ErrClock
	}
	p := s.h.Patients[patient]
	if p == nil {
		return ward.ErrNotFound
	}
	if !p.TransferValid(now, s.h.H) {
		return ward.ErrState
	}
	t := p.Transfer
	if !s.h.HasGrant(op, p.Ward) && !s.h.HasGrant(op, t.ToWard) {
		return ward.ErrNoGrant
	}
	if rb := s.h.ReservedBed(p); rb != nil {
		clearReserve(rb)
	}
	p.Transfer = nil
	s.h.LastNow = now
	return nil
}

// SetIso 变更患者及其房间的隔离标志。
func (s *Service) SetIso(now int64, op, patient string, on bool) error {
	s.h.Lock()
	defer s.h.Unlock()
	if !validNow(now) || op == "" || patient == "" {
		return ward.ErrInvalid
	}
	if now < s.h.LastNow {
		return ward.ErrClock
	}
	p := s.h.Patients[patient]
	if p == nil {
		return ward.ErrNotFound
	}
	if !s.h.HasGrant(op, p.Ward) {
		return ward.ErrNoGrant
	}
	if p.TransferValid(now, s.h.H) {
		return ward.ErrState
	}
	if on {
		w := s.h.Wards[p.Ward]
		r := w.Rooms[p.Room]
		sole := true
		for _, b := range r.OrderedBeds() {
			st := b.State(now, s.h.H)
			if st == ward.BedOccupied || st == ward.BedReserved {
				if b.Occupant != patient && b.ReservedBy != patient {
					sole = false
					break
				}
			}
		}
		if !sole {
			return ward.ErrNotSole
		}
	}
	p.Iso = on
	s.h.LastNow = now
	return nil
}

func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000 }

// clearReserve 让预留床直接回到空闲（预留失效/取消不经过清洁）。
func clearReserve(b *ward.Bed) {
	b.ReservedBy = ""
	b.ReserveAt = 0
	b.ReserveSex = 0
	b.ReserveIso = false
}
