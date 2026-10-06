package contacttracing

// 本文件实现“当前全部登记 + now → 接触关系”的纯函数式推导，
// 以及按病例缓存与按病房失效的索引。缓存仅用于避免重复计算：
// 任何可能改变结果的变更都会使对应病例失效，查询结果不依赖旧推导。

// sourceInfo 是某患者在某病例下的接触来源信息。
type sourceInfo struct {
	kind          ContactKind
	lastContactAt int64
}

// caseResult 是单个病例的推导结果。
type caseResult struct {
	derivedAt int64
	revoked   bool
	// sources: 其他患者 -> 在本病例下的等级与最后接触时刻。
	sources map[string]sourceInfo
	// closeSet 是密切接触者集合（供次密接判定使用）。
	closeSet map[string]struct{}
	// caseRooms 病例本人在传染期内有正时长停留的病房。
	caseRooms map[string]struct{}
	// closeRooms 密接在其暴露期内停留过的病房。
	closeRooms map[string]struct{}
}

type derivedCache struct {
	// results 按病例缓存推导结果。
	results map[string]*caseResult
	// roomCase 病房 -> 可能受该病房住宿影响的病例集合（只增，脏时惰性丢弃）。
	roomCase map[string]map[string]struct{}
	// patientCase 患者 -> 其作为病例本人或接触者参与过的病例集合。
	patientCase map[string]map[string]struct{}
	// roomCase 之外，记录病例本人与其所有病例，供首次状态查询补全候选。
	// （病例在创建时即登记到该索引，无需推导即可发现。）
	allCasesByPatient map[string]map[string]struct{}

	// 性能仪表：统计推导实际扫描的住宿区间数与推导病例数。
	staysScanned int64
	casesDerived int64
}

func newDerivedCache() *derivedCache {
	return &derivedCache{
		results:           map[string]*caseResult{},
		roomCase:          map[string]map[string]struct{}{},
		patientCase:       map[string]map[string]struct{}{},
		allCasesByPatient: map[string]map[string]struct{}{},
	}
}

func (d *derivedCache) registerCaseOwnership(patient, caseID string) {
	set := d.allCasesByPatient[patient]
	if set == nil {
		set = map[string]struct{}{}
		d.allCasesByPatient[patient] = set
	}
	set[caseID] = struct{}{}
}

// coRoomPatients 返回在给定病房集合中出现过（有任意住宿）的全部患者。
func (st *store) coRoomPatients(rooms map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	for room := range rooms {
		for _, stay := range st.roomStays[room] {
			out[stay.Patient] = struct{}{}
		}
	}
	return out
}

func roomsOf(st *store, patients map[string]struct{}) map[string]struct{} {
	rooms := map[string]struct{}{}
	for p := range patients {
		for _, stay := range st.stays[p] {
			rooms[stay.Room] = struct{}{}
		}
	}
	return rooms
}

// candidateCases 完备地返回某患者可能作为密接或次密接参与的病例候选，
// 不依赖任何推导结果。依据：接触链条深度恰为 2 跳（病例→密接→次密接），
// 每一跳都是“同病房”，因此病例本人必然落在该患者同病房邻域的两跳之内。
// 扫描量只与该患者及其同病房相关记录数有关，与全院规模无关。
func (st *store) candidateCases(patient string) map[string]struct{} {
	rooms0 := map[string]struct{}{}
	for _, stay := range st.stays[patient] {
		rooms0[stay.Room] = struct{}{}
	}
	// 第一跳：与该患者同病房的患者（病例本人或其密接）。
	patients1 := st.coRoomPatients(rooms0)
	patients1[patient] = struct{}{}
	// 第二跳：这些患者的同病房者（病例→密接→次密接链条另一端）。
	patients2 := st.coRoomPatients(roomsOf(st, patients1))

	owners := map[string]struct{}{}
	for p := range patients1 {
		owners[p] = struct{}{}
	}
	for p := range patients2 {
		owners[p] = struct{}{}
	}
	cand := map[string]struct{}{}
	for p := range owners {
		for _, c := range st.casesByPatient[p] {
			cand[c.ID] = struct{}{}
		}
	}
	return cand
}

func (d *derivedCache) indexCase(room, caseID string) {
	set := d.roomCase[room]
	if set == nil {
		set = map[string]struct{}{}
		d.roomCase[room] = set
	}
	set[caseID] = struct{}{}
}

func (d *derivedCache) indexPatient(patient, caseID string) {
	set := d.patientCase[patient]
	if set == nil {
		set = map[string]struct{}{}
		d.patientCase[patient] = set
	}
	set[caseID] = struct{}{}
}

// invalidateCase 丢弃单个病例的缓存（病例参数变更时）。
func (d *derivedCache) invalidateCase(caseID string) {
	delete(d.results, caseID)
}

// invalidateRoom 丢弃所有可能受该病房住宿影响的病例缓存，仅涉及索引命中的病例。
func (d *derivedCache) invalidateRoom(room string) {
	for caseID := range d.roomCase[room] {
		delete(d.results, caseID)
	}
}

func (st *store) stayEnd(stay *Stay, now int64) int64 {
	if stay.CheckOutOpen {
		return now
	}
	return stay.CheckOut
}

// infectiousWindow 返回病例在 now 下的传染期 [l,r)。
func infectiousWindow(c *Case, now int64) (int64, int64) {
	l := c.OnsetAt - InfectiousLeadMinutes
	if l < 0 {
		l = 0
	}
	r := now
	if !c.IsolationOpen {
		r = c.IsolatedAt
	}
	if r < l {
		r = l
	}
	return l, r
}

type overlapSeg struct{ l, r int64 }

// roomOverlapStays 返回指定病房内与窗口 [wl,wr) 有正时长重叠的患者住宿段，
// 按住宿逐条输出裁剪后的重叠区间（供与病例段求交）。
func (st *store) roomWindowStays(room string, wl, wr, now int64) []struct {
	patient string
	seg     overlapSeg
} {
	out := make([]struct {
		patient string
		seg     overlapSeg
	}, 0)
	for _, stay := range st.roomStays[room] {
		st.derived.staysScanned++
		end := st.stayEnd(stay, now)
		l := stay.CheckIn
		if wl > l {
			l = wl
		}
		r := end
		if wr < r {
			r = wr
		}
		if r > l {
			out = append(out, struct {
				patient string
				seg     overlapSeg
			}{stay.Patient, overlapSeg{l, r}})
		}
	}
	return out
}

func segIntersect(a, b overlapSeg) (overlapSeg, bool) {
	l := a.l
	if b.l > l {
		l = b.l
	}
	r := a.r
	if b.r < r {
		r = b.r
	}
	if r <= l {
		return overlapSeg{}, false
	}
	return overlapSeg{l, r}, true
}

func lastEnd(segs []overlapSeg) int64 {
	var last int64
	for _, seg := range segs {
		if seg.r > last {
			last = seg.r
		}
	}
	return last
}

type closeAgg struct {
	touch int64
	segs  []overlapSeg // 与病例在传染期内的逐段正重叠（跨病房）
}

type secondaryAgg struct {
	touch         int64
	lastContactAt int64
}

// deriveCase 计算单个病例在 now 下的全部接触关系。
func (st *store) deriveCase(caseID string, now int64) *caseResult {
	if r, ok := st.derived.results[caseID]; ok && r.derivedAt == now {
		return r
	}
	st.derived.casesDerived++
	c := st.cases[caseID]
	res := &caseResult{
		revoked:    c.Revoked,
		sources:    map[string]sourceInfo{},
		closeSet:   map[string]struct{}{},
		caseRooms:  map[string]struct{}{},
		closeRooms: map[string]struct{}{},
	}
	st.derived.indexPatient(c.Patient, caseID)

	if c.Revoked {
		res.derivedAt = now
		st.derived.results[caseID] = res
		return res
	}

	iL, iR := infectiousWindow(c, now)

	// 病例本人在传染期内、按病房的裁剪段。
	caseSegsByRoom := map[string][]overlapSeg{}
	for _, stay := range st.stays[c.Patient] {
		st.derived.staysScanned++
		end := st.stayEnd(stay, now)
		l := stay.CheckIn
		if iL > l {
			l = iL
		}
		r := end
		if iR < r {
			r = iR
		}
		if r > l {
			caseSegsByRoom[stay.Room] = append(caseSegsByRoom[stay.Room], overlapSeg{l, r})
			res.caseRooms[stay.Room] = struct{}{}
		}
	}

	// 第一圈：同病房且处于传染期内，跨病房/住宿段累计 >=120 分钟 → 密接。
	aggs := map[string]*closeAgg{}
	for room, caseSegs := range caseSegsByRoom {
		st.derived.indexCase(room, caseID)
		for _, item := range st.roomWindowStays(room, iL, iR, now) {
			if item.patient == c.Patient {
				continue
			}
			for _, cs := range caseSegs {
				if seg, ok := segIntersect(item.seg, cs); ok {
					agg := aggs[item.patient]
					if agg == nil {
						agg = &closeAgg{}
						aggs[item.patient] = agg
					}
					agg.touch += seg.r - seg.l
					agg.segs = append(agg.segs, seg)
				}
			}
		}
	}

	for other, agg := range aggs {
		if agg.touch >= MinContactMinutes {
			res.closeSet[other] = struct{}{}
			st.derived.indexPatient(other, caseID)
			res.sources[other] = sourceInfo{
				kind:          KindClose,
				lastContactAt: lastEnd(agg.segs),
			}
		}
	}

	// 第二圈：次密接。暴露期 [与病例首个正重叠起点, 确诊登记时刻)。
	secAggs := map[string]*secondaryAgg{}
	for closePatient := range res.closeSet {
		segs := aggs[closePatient].segs
		firstL := segs[0].l
		for _, seg := range segs[1:] {
			if seg.l < firstL {
				firstL = seg.l
			}
		}
		eL, eR := firstL, c.RegisteredAt
		if eR <= eL {
			continue
		}
		closeSegsByRoom := map[string][]overlapSeg{}
		for _, stay := range st.stays[closePatient] {
			st.derived.staysScanned++
			end := st.stayEnd(stay, now)
			l := stay.CheckIn
			if eL > l {
				l = eL
			}
			r := end
			if eR < r {
				r = eR
			}
			if r > l {
				closeSegsByRoom[stay.Room] = append(closeSegsByRoom[stay.Room], overlapSeg{l, r})
			}
		}
		for room, csegs := range closeSegsByRoom {
			res.closeRooms[room] = struct{}{}
			st.derived.indexCase(room, caseID)
			for _, item := range st.roomWindowStays(room, eL, eR, now) {
				other := item.patient
				if other == c.Patient || other == closePatient {
					continue
				}
				if _, isClose := res.closeSet[other]; isClose {
					continue
				}
				var overlap, lastR int64
				for _, cs := range csegs {
					if seg, ok := segIntersect(item.seg, cs); ok {
						overlap += seg.r - seg.l
						if seg.r > lastR {
							lastR = seg.r
						}
					}
				}
				if overlap > 0 {
					agg := secAggs[other]
					if agg == nil {
						agg = &secondaryAgg{}
						secAggs[other] = agg
					}
					agg.touch += overlap
					if lastR > agg.lastContactAt {
						agg.lastContactAt = lastR
					}
				}
			}
		}
	}

	for other, agg := range secAggs {
		if agg.touch >= MinContactMinutes {
			// 多密接叠加后达标：次密接不再升级为密接（已在 closeSet 中者已跳过）。
			st.derived.indexPatient(other, caseID)
			res.sources[other] = sourceInfo{
				kind:          KindSecondary,
				lastContactAt: agg.lastContactAt,
			}
		}
	}

	res.derivedAt = now
	st.derived.results[caseID] = res
	return res
}
