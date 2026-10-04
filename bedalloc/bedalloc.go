// Package bedalloc 实现按性别与隔离约束的确定性选床。
package bedalloc

import "ontology/ward"

// Choice 是一次选床结果。
type Choice struct {
	Room *ward.Room
	Bed  *ward.Bed
}

// Allocator 是无状态选床器；examined 记录最近一次 Select 考察的床位数。
type Allocator struct {
	h        *ward.Hospital
	examined int
}

// New 创建选床器。
func New(h *ward.Hospital) *Allocator {
	return &Allocator{h: h}
}

// Examined 返回最近一次 Select 考察过的床位数。
func (a *Allocator) Examined() int {
	return a.examined
}

// Select 按规则在目标病区选出一张空闲床；无候选返回 nil。
// 调用方必须持有 h 的锁。
func (a *Allocator) Select(now int64, w *ward.Ward, sex ward.Sex, iso bool) *Choice {
	a.examined = 0
	if w == nil {
		return nil
	}

	if iso {
		// 隔离：只考虑全部床位均空闲的房间（清洁中、有效预留均不算），
		// 总床数最少、并列房号字节序，取最小床号。
		var best *ward.Room
		bestBeds := 0
		for _, r := range w.OrderedRooms() {
			beds := r.OrderedBeds()
			allFree := true
			for _, b := range beds {
				a.examined++
				if b.State(now, a.h.H) != ward.BedFree {
					allFree = false
				}
			}
			if !allFree {
				continue
			}
			if best == nil || len(beds) < bestBeds {
				best, bestBeds = r, len(beds)
			}
		}
		if best == nil {
			return nil
		}
		return &Choice{Room: best, Bed: best.OrderedBeds()[0]}
	}

	// 非隔离：房间须无隔离标志、至少一张空闲床、在房者性别全部相同。
	// 先取已有在房者的房间（空闲床数最少、并列房号小），
	// 再取无任何在房者的房间（总床数最少、并列房号小）。
	var occupiedBest, emptyBest *ward.Room
	var occupiedBed, emptyBed *ward.Bed
	occupiedFreeMin := 0
	emptyTotalMin := 0
	for _, r := range w.OrderedRooms() {
		beds := r.OrderedBeds()
		free := 0
		var firstFree *ward.Bed
		occupants := 0
		sameSex := true
		flagged := false
		for _, b := range beds {
			a.examined++
			switch b.State(now, a.h.H) {
			case ward.BedFree:
				free++
				if firstFree == nil {
					firstFree = b
				}
			case ward.BedOccupied:
				occupants++
				p := a.h.Patients[b.Occupant]
				if p == nil || p.Sex != sex {
					sameSex = false
				}
				if p != nil && p.Iso {
					flagged = true
				}
			case ward.BedReserved:
				occupants++
				if b.ReserveSex != sex {
					sameSex = false
				}
				if b.ReserveIso {
					flagged = true
				}
			}
		}
		if flagged || free == 0 || !sameSex {
			continue
		}
		if occupants > 0 {
			if occupiedBest == nil || free < occupiedFreeMin {
				occupiedBest, occupiedFreeMin, occupiedBed = r, free, firstFree
			}
		} else if emptyBest == nil || len(beds) < emptyTotalMin {
			emptyBest, emptyTotalMin, emptyBed = r, len(beds), firstFree
		}
	}

	r := occupiedBest
	bed := occupiedBed
	if r == nil {
		r = emptyBest
		bed = emptyBed
	}
	if r == nil {
		return nil
	}
	return &Choice{Room: r, Bed: bed}
}
