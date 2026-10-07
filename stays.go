package ontology

import "math"

// stay 一条住宿记录：患者在某病房的 [in, out) 住宿区间。
// open 为 true 时表示出住时刻未登记，区间视为持续到当前 now。
type stay struct {
	patient string
	ward    string
	in      int64
	out     int64 // 仅 !open 时有效
	open    bool
}

// end 返回该住宿区间在指定 now 下的结束时刻（开区间端点）。
func (s *stay) end(now int64) int64 {
	if s.open {
		return now
	}
	return s.out
}

// stayStore 住宿记录存储：按患者与按病房双索引。
// 住宿记录不可删除，只允许追加（含追补过去的区间）。
type stayStore struct {
	byPatient map[string][]*stay
	byWard    map[string][]*stay
}

func newStayStore() *stayStore {
	return &stayStore{
		byPatient: make(map[string][]*stay),
		byWard:    make(map[string][]*stay),
	}
}

func (ss *stayStore) ofPatient(p string) []*stay { return ss.byPatient[p] }

func (ss *stayStore) ofWard(w string) []*stay { return ss.byWard[w] }

// openStay 返回患者当前未出住的记录，无则 nil。
// 不变式：同一患者至多一条 open 记录（由 Admit 保证）。
func (ss *stayStore) openStay(p string) *stay {
	for _, s := range ss.byPatient[p] {
		if s.open {
			return s
		}
	}
	return nil
}

// hasStay 返回患者是否曾有过任何住宿记录。
func (ss *stayStore) hasStay(p string) bool { return len(ss.byPatient[p]) > 0 }

// overlapsPatient 检查新区间 [in, out) 是否与该患者已有住宿区间重叠。
// 对 open 记录其结束时刻按 +∞ 处理（尚未出住，可能延伸到任意将来）。
func (ss *stayStore) overlapsPatient(p string, in, out int64) bool {
	for _, s := range ss.byPatient[p] {
		se := s.out
		if s.open {
			se = math.MaxInt64
		}
		if s.in < out && in < se {
			return true
		}
	}
	return false
}

func (ss *stayStore) add(s *stay) {
	ss.byPatient[s.patient] = append(ss.byPatient[s.patient], s)
	ss.byWard[s.ward] = append(ss.byWard[s.ward], s)
}
