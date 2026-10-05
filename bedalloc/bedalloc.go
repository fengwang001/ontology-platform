// Package bedalloc 按性别与隔离约束在病区内确定性地选床。
//
// 每个房间只扫描一遍床位，考察的床位数恒等于目标病区床位总数，
// 由非导出计数器 examined 记录，与其他病区的规模无关。
// 选择只依赖 (比较键, 房号字节序) 的全序，与 map 迭代顺序无关，可精确复现。
package bedalloc

import (
	"sync/atomic"

	"ontology/ward"
)

// examined 非导出计数器：本次 Find 考察的床位数（不超过病区床位总数）。
var examined atomic.Int64

// Find 在病区 w 内为 (sex, iso) 的患者选床，返回房号与床号（1 起）。
// h 为预留有效期，now 为当前时刻；床位状态按 now 的纯函数判定。
func Find(w *ward.Ward, now, h int, sex ward.Sex, iso bool) (room string, bed int, ok bool) {
	examined.Store(0)
	if iso {
		return findIso(w, now, h)
	}
	return findRegular(w, now, h, sex)
}

// roomView 单次扫描得到的房间派生状态。
type roomView struct {
	id     string
	free   int // 空闲床数
	beds   int // 总床数
	minBed int // 最小空闲床号（1 起，0 表示无空闲）
	occ    int // 在房者数（占用 + 有效预留）
	same   bool
	isoOn  bool
}

// scan 单次遍历房间全部床位，统计选床所需的全部信息。
func scan(id string, r *ward.Room, now, h int, sex ward.Sex) roomView {
	v := roomView{id: id, beds: len(r.Beds), same: true}
	for i := range r.Beds {
		b := &r.Beds[i]
		examined.Add(1)
		switch b.EffStatus(now, h) {
		case ward.Free:
			v.free++
			if v.minBed == 0 {
				v.minBed = i + 1
			}
		case ward.Occupied, ward.Reserved:
			v.occ++
			if b.Sex != sex {
				v.same = false
			}
			if r.IsoBy != "" && b.Patient == r.IsoBy {
				v.isoOn = true
			}
		}
	}
	return v
}

// findRegular 非隔离患者：候选房间无隔离标志、有空闲床、在房者同性。
// 先取有在房者中空闲床最少者，再取无在房者中总床数最少者，并列均取房号小者。
func findRegular(w *ward.Ward, now, h int, sex ward.Sex) (string, int, bool) {
	var occ, empty roomView
	hasOcc, hasEmpty := false, false
	for id, r := range w.Rooms {
		v := scan(id, r, now, h, sex)
		if v.isoOn || v.free == 0 || !v.same {
			continue
		}
		if v.occ > 0 {
			if !hasOcc || v.free < occ.free || (v.free == occ.free && v.id < occ.id) {
				occ, hasOcc = v, true
			}
		} else {
			if !hasEmpty || v.beds < empty.beds || (v.beds == empty.beds && v.id < empty.id) {
				empty, hasEmpty = v, true
			}
		}
	}
	if hasOcc {
		return occ.id, occ.minBed, true
	}
	if hasEmpty {
		return empty.id, empty.minBed, true
	}
	return "", 0, false
}

// findIso 隔离患者：只选全部床位均空闲的房间（清洁中的床不算），
// 取总床数最少、并列房号小者，占床号最小的床。
func findIso(w *ward.Ward, now, h int) (string, int, bool) {
	best := ""
	bestBeds := 0
	for id, r := range w.Rooms {
		allFree := true
		for i := range r.Beds {
			examined.Add(1)
			if r.Beds[i].EffStatus(now, h) != ward.Free {
				allFree = false
			}
		}
		if !allFree {
			continue
		}
		if best == "" || len(r.Beds) < bestBeds || (len(r.Beds) == bestBeds && id < best) {
			best, bestBeds = id, len(r.Beds)
		}
	}
	if best == "" {
		return "", 0, false
	}
	return best, 1, true
}
