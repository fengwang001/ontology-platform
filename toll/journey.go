package toll

import (
	"fmt"
	"sort"
	"time"
)

// classChange 一次带生效时刻的车型变更。
type classChange struct {
	Effective time.Time
	Class     VehicleClass
	Seq       int // 接受顺序，生效时刻相同者后到的生效
}

// storedRecord 一条已登记的门架记录。
type storedRecord struct {
	Gantry  GantryID
	Kind    RecordKind
	Time    time.Time // 记录自身时刻
	Arrival time.Time // 到达（操作）时刻
	Seq     int
	Status  RecordStatus
}

func (r *storedRecord) view() RecordView {
	return RecordView{Gantry: r.Gantry, Kind: r.Kind, Time: r.Time, Arrival: r.Arrival, Status: r.Status}
}

// recKey 重复判定键：同一车辆同一门架同类记录。
type recKey struct {
	g GantryID
	k RecordKind
}

// monthAgg 车辆某自然月的汇总。
type monthAgg struct {
	Collected         int64
	CappedUncollected int64
	UnrefundedExcess  int64
}

// vehicle 车辆状态。
type vehicle struct {
	id          VehicleID
	baseClass   VehicleClass
	changes     []classChange
	journeys    []*Journey
	open        *Journey // 最近一个未结算行程
	openCount   int
	lastSettled *Journey // 最近完成结算的行程
	kept        map[recKey][]*storedRecord // 已保留记录（去重判定用）
	dups        []*storedRecord            // 重复记录（只登记）
	orphans     []*storedRecord
	months      map[string]*monthAgg
}

func newVehicle(id VehicleID, class VehicleClass) *vehicle {
	return &vehicle{
		id:        id,
		baseClass: class,
		kept:      map[recKey][]*storedRecord{},
		months:    map[string]*monthAgg{},
	}
}

// classAt 返回时刻 t 对应的车型：生效时刻不大于 t 的最近一次变更；
// 经过时刻恰等于生效时刻时按新车型。
func (v *vehicle) classAt(t time.Time) VehicleClass {
	class := v.baseClass
	best := -1
	for i, ch := range v.changes {
		if ch.Effective.After(t) {
			continue
		}
		if best < 0 || ch.Effective.After(v.changes[best].Effective) ||
			(ch.Effective.Equal(v.changes[best].Effective) && ch.Seq > v.changes[best].Seq) {
			best = i
		}
	}
	if best >= 0 {
		class = v.changes[best].Class
	}
	return class
}

// isDup 判定记录是否重复：同门架同类已保留记录中存在间隔严格小于重复窗口者。
// 间隔恰等于窗口长度视为两条独立记录。
func (v *vehicle) isDup(cfg Config, g GantryID, k RecordKind, t time.Time) bool {
	for _, r := range v.kept[recKey{g, k}] {
		d := t.Sub(r.Time)
		if d < 0 {
			d = -d
		}
		if d < cfg.DedupWindow {
			return true
		}
	}
	return false
}

func (v *vehicle) month(key string) *monthAgg {
	m, ok := v.months[key]
	if !ok {
		m = &monthAgg{}
		v.months[key] = m
	}
	return m
}

// markKept 将记录登记为已保留，参与后续去重判定。
func (v *vehicle) markKept(r *storedRecord) {
	k := recKey{r.Gantry, r.Kind}
	v.kept[k] = append(v.kept[k], r)
}

// rescanOpen 在最近未结算行程被结算/关闭后，重新定位最近的未结算行程。
func (v *vehicle) rescanOpen() {
	v.open = nil
	for i := len(v.journeys) - 1; i >= 0; i-- {
		if v.journeys[i].status == JourneyOpen {
			v.open = v.journeys[i]
			return
		}
	}
}

func monthKey(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01")
}

// Journey 一次行程：入口记录开始、出口记录结束。
type Journey struct {
	ID     string
	v      *vehicle
	status JourneyStatus
	entry  *storedRecord
	exit   *storedRecord
	passes []*storedRecord // 已保留的中间记录（到达顺序）
	// 过期迟到记录仍登记在行程内，但不参与路径推定。
	expired []*storedRecord

	path              []GantryID
	fullAmount        int64
	collected         int64
	cappedUncollected int64
	unrefundedExcess  int64
	adjustments       []Adjustment
}

// waypoints 组装路径推定的途经点：入口 + 按 (时刻, 门架, 序号) 排序的中间记录 + 出口，
// 以及每个途经点经过时刻对应的车型。
func (j *Journey) waypoints(exit *storedRecord) ([]GantryID, []VehicleClass) {
	ps := make([]*storedRecord, len(j.passes))
	copy(ps, j.passes)
	sort.Slice(ps, func(a, b int) bool {
		if !ps[a].Time.Equal(ps[b].Time) {
			return ps[a].Time.Before(ps[b].Time)
		}
		if ps[a].Gantry != ps[b].Gantry {
			return ps[a].Gantry < ps[b].Gantry
		}
		return ps[a].Seq < ps[b].Seq
	})
	wps := make([]GantryID, 0, len(ps)+2)
	classes := make([]VehicleClass, 0, len(ps)+1)
	wps = append(wps, j.entry.Gantry)
	classes = append(classes, j.v.classAt(j.entry.Time))
	for _, p := range ps {
		wps = append(wps, p.Gantry)
		classes = append(classes, j.v.classAt(p.Time))
	}
	wps = append(wps, exit.Gantry)
	return wps, classes
}

// infer 以候选出口记录推定计费路径与全额。
func (j *Journey) infer(n *Network, exit *storedRecord) ([]GantryID, int64, bool) {
	wps, classes := j.waypoints(exit)
	return n.Infer(wps, classes)
}

// settle 出口结算：推定路径并按月度封顶收取。返回结算调整明细。
func (j *Journey) settle(n *Network, cfg Config, exit *storedRecord, at time.Time, seq int) (Adjustment, error) {
	path, full, ok := j.infer(n, exit)
	if !ok {
		return Adjustment{}, fmt.Errorf("%w: %s -> %s", ErrPathUnreachable, j.entry.Gantry, exit.Gantry)
	}
	j.exit = exit
	j.status = JourneySettled
	j.path = path
	j.fullAmount = full
	m := j.v.month(monthKey(exit.Time, cfg.Location))
	charge := min64(full, cfg.MonthlyCap-m.Collected)
	j.collected = charge
	j.cappedUncollected = full - charge
	m.Collected += charge
	m.CappedUncollected += full - charge
	adj := Adjustment{
		Seq: seq, Kind: AdjSettle, Amount: charge, CappedPart: full - charge,
		FullAmount: full, Path: append([]GantryID(nil), path...), At: at,
	}
	j.adjustments = append(j.adjustments, adj)
	return adj, nil
}

// recompute 迟到记录触发的重算：与已收金额比较产生补扣或退款。
// 重算不可达时保留原金额与路径，不产生调整。
func (j *Journey) recompute(n *Network, cfg Config, at time.Time, seq int) *Adjustment {
	path, full, ok := j.infer(n, j.exit)
	if !ok {
		return nil
	}
	j.path = path
	j.fullAmount = full
	m := j.v.month(monthKey(j.exit.Time, cfg.Location))
	delta := full - j.collected
	switch {
	case delta > 0:
		charge := min64(delta, cfg.MonthlyCap-m.Collected)
		j.collected += charge
		j.cappedUncollected += delta - charge
		m.Collected += charge
		m.CappedUncollected += delta - charge
		adj := Adjustment{
			Seq: seq, Kind: AdjBackCharge, Amount: charge, CappedPart: delta - charge,
			FullAmount: full, Path: append([]GantryID(nil), path...), At: at,
		}
		j.adjustments = append(j.adjustments, adj)
		return &j.adjustments[len(j.adjustments)-1]
	case delta < 0:
		want := -delta
		refund := min64(want, m.Collected)
		j.collected -= refund
		j.unrefundedExcess += want - refund
		m.Collected -= refund
		m.UnrefundedExcess += want - refund
		adj := Adjustment{
			Seq: seq, Kind: AdjRefund, Amount: refund, UnrefundedPart: want - refund,
			FullAmount: full, Path: append([]GantryID(nil), path...), At: at,
		}
		j.adjustments = append(j.adjustments, adj)
		return &j.adjustments[len(j.adjustments)-1]
	}
	return nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
