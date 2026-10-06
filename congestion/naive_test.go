package congestion_test

import (
	"math"
	"sort"
	"time"
)

// naiveZone / naiveVeh / naiveModel 是与生产实现完全独立编写的朴素模型：
// 每条查询都从全部原始操作出发，逐车逐日全量重算。

type naiveZone struct {
	fee      int64
	cells    map[string]bool
	startMin int
	endMin   int
}

type naiveQual struct {
	kind       int // 0 resident,1 disabled,2 newenergy
	zone       string
	discount   int64
	start, end time.Time
}

type naiveEntry struct {
	zone string
	at   time.Time
}

type naiveVehicle struct {
	plates []struct {
		at    time.Time
		plate string
	}
	quals   []naiveQual
	entries []naiveEntry
	dispute map[string]bool
}

type naiveModel struct {
	loc   *time.Location
	cap   int64
	basis int64
	zones map[string]naiveZone
	vehs  map[string]*naiveVehicle
	// 冻结值与关闭后的“一次性调整”直接按操作效果记录
	frozenPay   map[string]int64 // vehicle|day
	frozenLines map[string][]naiveLine
	closedAdj   map[string]int64
}

type naiveLine struct {
	zone   string
	gross  int64
	pay    int64
	reason string
}

func newNaive(loc *time.Location, capAmt int64, basis int64) *naiveModel {
	return &naiveModel{
		loc: loc, cap: capAmt, basis: basis,
		zones:       map[string]naiveZone{},
		vehs:        map[string]*naiveVehicle{},
		frozenPay:   map[string]int64{},
		frozenLines: map[string][]naiveLine{},
		closedAdj:   map[string]int64{},
	}
}

func (m *naiveModel) contains(inner, outer string) bool {
	a := m.zones[inner].cells
	b := m.zones[outer].cells
	for c := range a {
		if !b[c] {
			return false
		}
	}
	return true
}

func (m *naiveModel) validAdd(zid string, cells map[string]bool) bool {
	for id, ex := range m.zones {
		inter := false
		for c := range cells {
			if ex.cells[c] {
				inter = true
			}
		}
		if !inter {
			continue
		}
		// 相交：必须一个包含另一个
		aInB := true
		for c := range cells {
			if !ex.cells[c] {
				aInB = false
			}
		}
		bInA := true
		for c := range ex.cells {
			if !cells[c] {
				bInA = false
			}
		}
		if id == zid || (!aInB && !bInA) {
			return false
		}
	}
	return true
}

func (m *naiveModel) dayStr(t time.Time) string { return t.In(m.loc).Format("2006-01-02") }

// computeDay 朴素逐日全量重算，返回应付与逐行。
func (m *naiveModel) computeDay(v *naiveVehicle, day string) (int64, []naiveLine) {
	es := make([]naiveEntry, 0)
	for _, e := range v.entries {
		if m.dayStr(e.at) == day {
			es = append(es, e)
		}
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].at.Before(es[j].at) })

	seen := map[string]bool{}
	var total int64
	var lines []naiveLine
	for _, e := range es {
		ids := []string{e.zone}
		var anc []string
		for id := range m.zones {
			if id != e.zone && m.contains(e.zone, id) {
				anc = append(anc, id)
			}
		}
		sort.Strings(anc)
		ids = append(ids, anc...)
		for _, zid := range ids {
			z := m.zones[zid]
			local := e.at.In(m.loc)
			min := local.Hour()*60 + local.Minute()
			if min < z.startMin || min >= z.endMin {
				continue
			}
			if seen[zid] {
				continue
			}
			seen[zid] = true
			net, reason := m.apply(v, zid, e.at, z.fee)
			l := naiveLine{zone: zid, gross: z.fee, reason: reason}
			switch {
			case total >= m.cap:
				l.pay, l.reason = 0, "capped:daily_cap_already_reached"
			case total+net > m.cap:
				l.pay = m.cap - total
				total = m.cap
				if reason == "" {
					l.reason = "capped:daily_cap"
				} else {
					l.reason = "capped_at:" + reason
				}
			default:
				l.pay = net
				total += net
				if l.reason == "" {
					l.reason = "full_fee"
				}
			}
			lines = append(lines, l)
		}
	}
	return total, lines
}

func (m *naiveModel) apply(v *naiveVehicle, zid string, at time.Time, fee int64) (int64, string) {
	hasDis, hasNE := false, false
	resDiscount := int64(-1)
	for _, q := range v.quals {
		if at.Before(q.start) || !at.Before(q.end) {
			continue
		}
		switch q.kind {
		case 1:
			hasDis = true
		case 2:
			hasNE = true
		default:
			if q.zone == zid {
				resDiscount = q.discount
			}
		}
	}
	if hasDis {
		return 0, "exempt:disabled"
	}
	if hasNE {
		return 0, "exempt:new_energy"
	}
	if resDiscount >= 0 {
		relief := int64(math.Ceil(float64(fee*resDiscount) / float64(m.basis)))
		net := fee - relief
		if net < 0 {
			net = 0
		}
		return net, "resident_discount"
	}
	return fee, ""
}
