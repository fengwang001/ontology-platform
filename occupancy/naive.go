package occupancy

import (
	"fmt"
	"sort"
)

// naivePermit 是朴素模型中的许可记录，保留当前生效区间、状态与历史片段。
type naivePermit struct {
	id      int64
	road    string
	lanes   int
	start   int64
	end     int64
	prio    Priority
	status  Status
	appSeq  int64
	archive [][2]int64
}

// NaiveModel 用线性全量重判独立复现 Service 的全部语义。
// 它不使用任何索引结构，每次判定都遍历全部许可，刻意简单直白。
type NaiveModel struct {
	lanes     map[string]int
	corr      map[string]string
	detourOf  map[string][]string
	revOf     map[string][]string
	corrCap   map[string]int
	permits   map[int64]*naivePermit
	order     []int64
	clock     int64
	nextID    int64
	seq       int64
	heldV     []*naivePermit
	tempEm    []*naivePermit // 抢占重审时临时既存的应急占用
	tempLanes int
}

// NewNaiveModel 与 NewService 接受同一配置；配置不合法时返回同样错误码。
func NewNaiveModel(cfg Config) (*NaiveModel, CodeError) {
	m := &NaiveModel{
		lanes: map[string]int{}, corr: map[string]string{},
		detourOf: map[string][]string{}, revOf: map[string][]string{},
		corrCap: map[string]int{}, permits: map[int64]*naivePermit{},
	}
	if len(cfg.Roads) == 0 {
		return nil, fail(ErrInvalidParams, "no roads configured")
	}
	for _, r := range cfg.Roads {
		if r.ID == "" {
			return nil, fail(ErrInvalidParams, "empty road id")
		}
		if _, dup := m.lanes[r.ID]; dup {
			return nil, fail(ErrInvalidParams, "duplicate road: "+r.ID)
		}
		if r.Lanes <= 0 {
			return nil, fail(ErrInvalidParams, "lanes")
		}
		if r.Corridor == "" {
			return nil, fail(ErrInvalidParams, "corridor")
		}
		m.lanes[r.ID] = r.Lanes
		m.corr[r.ID] = r.Corridor
		m.detourOf[r.ID] = append([]string(nil), r.Detour...)
	}
	for _, r := range cfg.Roads {
		seen := map[string]bool{}
		for _, d := range r.Detour {
			if _, ok := m.lanes[d]; !ok {
				return nil, fail(ErrInvalidParams, "detour road")
			}
			if d == r.ID || seen[d] {
				return nil, fail(ErrInvalidParams, "detour self/dup")
			}
			seen[d] = true
			m.revOf[d] = append(m.revOf[d], r.ID)
		}
	}
	for c, n := range cfg.CorridorCap {
		if n <= 0 {
			return nil, fail(ErrInvalidParams, "cap")
		}
		m.corrCap[c] = n
	}
	for _, c := range m.corr {
		if _, ok := m.corrCap[c]; !ok {
			m.corrCap[c] = -1
		}
	}
	return m, CodeError{}
}

func ov(a0, a1, b0, b1 int64) bool { return a0 < b1 && b0 < a1 }

func (m *NaiveModel) nextSeq() int64 { m.seq++; return m.seq }

func (m *NaiveModel) snapshot(p *naivePermit) Permit {
	return Permit{
		ID: p.id, Road: p.road, Lanes: p.lanes,
		Interval: Interval{p.start, p.end}, Priority: p.prio,
		Status: p.status, ApprovedSeq: p.appSeq,
	}
}

// activeOthers 枚举某路段与候选时段相交的已批许可（含应急）。
func (m *NaiveModel) activeOthers(road string, s, e int64, exclude int64) []*naivePermit {
	var out []*naivePermit
	for _, id := range m.order {
		p := m.permits[id]
		if p.status != Approved || p.id == exclude || p.road != road {
			continue
		}
		if ov(p.start, p.end, s, e) {
			out = append(out, p)
		}
	}
	for _, p := range m.tempEm {
		if p.road == road && p.id != exclude && ov(p.start, p.end, s, e) {
			out = append(out, p)
		}
	}
	return out
}

func (m *NaiveModel) sameRoad(road string, lanes int, s, e int64, exclude int64) checkResult {
	total := lanes
	n := 0
	for _, p := range m.activeOthers(road, s, e, exclude) {
		total += p.lanes
		n++
	}
	if total > m.lanes[road] {
		return checkResult{ErrSameRoadConflict,
			fmt.Sprintf("naive total %d > %d with %d", total, m.lanes[road], n)}
	}
	return checkResult{}
}

func (m *NaiveModel) detourCheck(road string, full bool, s, e int64, exclude int64) checkResult {
	if full {
		for _, d := range m.detourOf[road] {
			for _, p := range m.activeOthers(d, s, e, exclude) {
				if p.prio == Regular {
					return checkResult{ErrDetourConflict,
						fmt.Sprintf("naive detour %s permit %d", d, p.id)}
				}
			}
		}
	}
	for _, up := range m.revOf[road] {
		for _, p := range m.activeOthers(up, s, e, exclude) {
			if p.prio == Regular && p.lanes == m.lanes[up] {
				return checkResult{ErrDetourConflict,
					fmt.Sprintf("naive upstream %s permit %d", up, p.id)}
			}
		}
	}
	return checkResult{}
}

func (m *NaiveModel) corridorCheck(c string, s, e int64, exclude int64) checkResult {
	type ev struct {
		t    int64
		open bool
	}
	var es []ev
	es = append(es, ev{s, true})
	for _, id := range m.order {
		p := m.permits[id]
		if p.status != Approved || p.prio != Regular || p.id == exclude || m.corr[p.road] != c {
			continue
		}
		if ov(p.start, p.end, s, e) {
			es = append(es, ev{p.start, true}, ev{p.end, false})
		}
	}
	for i := 1; i < len(es); i++ {
		for j := i; j > 0; j-- {
			a, b := es[j-1], es[j]
			less := a.t < b.t || (a.t == b.t && !a.open && b.open)
			if !less {
				es[j-1], es[j] = b, a
			}
		}
	}
	cur, peak := 0, 0
	for _, x := range es {
		if x.open {
			cur++
			if cur > peak {
				peak = cur
			}
		} else {
			cur--
		}
	}
	if peak > m.corrCap[c] {
		return checkResult{ErrCorridorCap, fmt.Sprintf("naive peak %d", peak)}
	}
	return checkResult{}
}

func (m *NaiveModel) regular(road string, lanes int, s, e int64, exclude int64) checkResult {
	if c := m.sameRoad(road, lanes, s, e, exclude); c.fail() {
		return c
	}
	if c := m.detourCheck(road, lanes == m.lanes[road], s, e, exclude); c.fail() {
		return c
	}
	return m.corridorCheck(m.corr[road], s, e, exclude)
}

// Apply 朴素受理。
func (m *NaiveModel) Apply(req ApplyRequest) ApplyResult {
	if req.Priority != Regular && req.Priority != Emergency {
		return ApplyResult{Reason: fail(ErrInvalidParams, "prio")}
	}
	if req.Lanes <= 0 || req.Start < 0 || req.End <= req.Start {
		return ApplyResult{Reason: fail(ErrInvalidParams, "params")}
	}
	if req.OpTime < m.clock {
		return ApplyResult{Reason: fail(ErrClockRollback, "clock")}
	}
	rl, ok := m.lanes[req.Road]
	if !ok {
		return ApplyResult{Reason: fail(ErrRoadNotFound, req.Road)}
	}
	if req.Lanes > rl {
		return ApplyResult{Reason: fail(ErrLanesExceed, "lanes")}
	}
	if req.Start < req.OpTime {
		return ApplyResult{Reason: fail(ErrStartBeforeNow, "start")}
	}
	var cr checkResult
	if req.Priority == Emergency {
		// 应急同路段检查只与其他应急比：先临时摘除常规受害者，失败原样恢复。
		var held []*naivePermit
		for _, p := range m.activeOthers(req.Road, req.Start, req.End, 0) {
			if p.prio == Regular {
				held = append(held, p)
				p.status = PendingReschedule
			}
		}
		cr = m.sameRoad(req.Road, req.Lanes, req.Start, req.End, 0)
		if cr.fail() {
			for _, p := range held {
				p.status = Approved
			}
		}
		m.heldV = held
		m.tempLanes = req.Lanes
	} else {
		cr = m.regular(req.Road, req.Lanes, req.Start, req.End, 0)
	}
	if cr.fail() {
		return ApplyResult{Reason: cr.err()}
	}

	seq := m.nextSeq()
	m.clock = req.OpTime
	var pre []PreemptRecord
	if req.Priority == Emergency {
		// 受害者已在检查阶段摘除并置为 PendingReschedule，preempt 复用它们。
		pre = m.reschedule(req.Start, req.End)
		m.heldV = nil
	}
	m.nextID++
	p := &naivePermit{
		id: m.nextID, road: req.Road, lanes: req.Lanes,
		start: req.Start, end: req.End, prio: req.Priority,
		status: Approved, appSeq: seq,
	}
	m.permits[p.id] = p
	m.order = append(m.order, p.id)
	return ApplyResult{PermitID: p.id, Permit: m.snapshot(p), Preempted: pre}
}

// reschedule 处理已在受理检查阶段摘除的常规受害者，按原批准序顺延重审。
// 应急许可 [es0,es1) 视为既存占用参与同路段判定。
func (m *NaiveModel) reschedule(es0, es1 int64) []PreemptRecord {
	victims := append([]*naivePermit(nil), m.heldV...)
	sort.SliceStable(victims, func(i, j int) bool {
		return victims[i].appSeq < victims[j].appSeq
	})
	var recs []PreemptRecord
	for _, p := range victims {
		rec := PreemptRecord{PermitID: p.id}
		oldS, oldE := p.start, p.end
		var ns, ne int64
		if oldS < es0 {
			rec.Truncated = true
			p.archive = append(p.archive, [2]int64{oldS, es0})
			ns = es1
			ne = es1 + (oldE - es0)
		} else {
			ns = oldS
			if ns < es1 {
				ns = es1
			}
			ne = ns + (oldE - oldS)
		}
		rec.NewStart, rec.NewEnd = ns, ne
		p.status = PendingReschedule
		p.start, p.end = ns, ne
		temp := &naivePermit{id: -1, road: p.road, lanes: m.tempLanes, start: es0, end: es1,
			prio: Emergency, status: Approved}
		m.tempEm = append(m.tempEm, temp)
		cr := m.regular(p.road, p.lanes, ns, ne, p.id)
		m.tempEm = m.tempEm[:len(m.tempEm)-1]
		if cr.fail() {
			rec.Reason = cr.err()
		} else {
			rec.Approved = true
			p.status = Approved
		}
		recs = append(recs, rec)
	}
	return recs
}

// Extend 朴素延期。
func (m *NaiveModel) Extend(req ExtendRequest) MutationResult {
	if req.OpTime < m.clock {
		return MutationResult{Reason: fail(ErrClockRollback, "clock")}
	}
	p, ok := m.permits[req.PermitID]
	if !ok {
		return MutationResult{Reason: fail(ErrPermitNotFound, "id")}
	}
	if p.status != Approved || req.OpTime >= p.end {
		return MutationResult{Reason: fail(ErrPermitEnded, "ended")}
	}
	if req.NewEnd <= p.start {
		return MutationResult{Reason: fail(ErrInvalidParams, "newend")}
	}
	m.nextSeq()
	if req.NewEnd <= p.end {
		p.end = req.NewEnd
		m.clock = req.OpTime
		return MutationResult{Permit: m.snapshot(p)}
	}
	var cr checkResult
	if p.prio == Emergency {
		cr = m.sameRoad(p.road, p.lanes, p.end, req.NewEnd, p.id)
	} else {
		cr = m.regular(p.road, p.lanes, p.end, req.NewEnd, p.id)
	}
	if cr.fail() {
		m.seq--
		return MutationResult{Reason: cr.err()}
	}
	p.end = req.NewEnd
	m.clock = req.OpTime
	return MutationResult{Permit: m.snapshot(p)}
}

// Revoke 朴素撤销。
func (m *NaiveModel) Revoke(req RevokeRequest) MutationResult {
	if req.OpTime < m.clock {
		return MutationResult{Reason: fail(ErrClockRollback, "clock")}
	}
	p, ok := m.permits[req.PermitID]
	if !ok {
		return MutationResult{Reason: fail(ErrPermitNotFound, "id")}
	}
	if p.status == Revoked {
		return MutationResult{Reason: fail(ErrPermitEnded, "revoked")}
	}
	if p.status == Approved && req.OpTime >= p.end {
		return MutationResult{Reason: fail(ErrPermitEnded, "ended")}
	}
	m.nextSeq()
	p.status = Revoked
	m.clock = req.OpTime
	return MutationResult{Permit: m.snapshot(p)}
}

// Query 朴素查询，遍历所有许可当前区间与历史归档。
func (m *NaiveModel) Query(req QueryRequest) (QueryResult, CodeError) {
	if _, ok := m.lanes[req.Road]; !ok {
		return QueryResult{}, fail(ErrRoadNotFound, req.Road)
	}
	res := QueryResult{}
	for _, id := range m.order {
		p := m.permits[id]
		if p.road != req.Road {
			continue
		}
		if p.status == Revoked {
			// 撤销后归档片段也不应再视为“生效许可”，与 Service 撤销语义一致。
			continue
		}
		hit := false
		if p.status == Approved && p.start <= req.At && req.At < p.end {
			hit = true
		}
		for _, a := range p.archive {
			if a[0] <= req.At && req.At < a[1] {
				hit = true
			}
		}
		if hit {
			res.ClosedLanes += p.lanes
			res.Active = append(res.Active, m.snapshot(p))
		}
	}
	sort.Slice(res.Active, func(i, j int) bool { return res.Active[i].ID < res.Active[j].ID })
	return res, CodeError{}
}

func (m *NaiveModel) String() string {
	return fmt.Sprintf("NaiveModel(permits=%d)", len(m.permits))
}
