package tou_test

import (
	"sort"
	"time"
)

// naiveModel 是按需求逐句写成的独立参考实现：不维护任何增量缓存，
// 每次查账/封账都从全部读数、版本与节假日暴力重算，用于差分测试。
type naiveModel struct {
	loc      *time.Location
	versions []naiveVersion
	holidays map[string]bool
	points   map[string][]naiveReading
	closed   map[string]map[int64]bool
	snaps    map[string]map[int64]naiveBill
}

type naiveVersion struct {
	at  time.Time
	sch [3][]naivePeriod
}

type naivePeriod struct {
	start int
	price int64
}

type naiveReading struct {
	at time.Time
	wh int64
}

type naiveSeg struct {
	lo, hi   time.Time
	day      int
	startSec int
	price    int64
	billable bool
}

type naiveBill struct {
	entries []naiveEntry
	totalWh int64
	amount  int64
	unbill  int64
}

type naiveEntry struct {
	day        int
	startSec   int
	price      int64
	wh, amount int64
}

func newNaive(loc *time.Location) *naiveModel {
	return &naiveModel{
		loc:      loc,
		holidays: map[string]bool{},
		points:   map[string][]naiveReading{},
		closed:   map[string]map[int64]bool{},
		snaps:    map[string]map[int64]naiveBill{},
	}
}

func naiveKey(t time.Time) int64 {
	y, m, _ := t.Date()
	return int64(y)*12 + int64(m) - 1
}

func (m *naiveModel) maxClosedEnd(point string) (time.Time, bool) {
	set := m.closed[point]
	if len(set) == 0 {
		return time.Time{}, false
	}
	var mk int64
	for k := range set {
		if mk == 0 || k > mk {
			mk = k
		}
	}
	y, mo := int(mk/12), time.Month(mk%12+1)
	return time.Date(y, mo, 1, 0, 0, 0, 0, m.loc).AddDate(0, 1, 0), true
}

func validNaiveSchedules(sch [3][]naivePeriod) bool {
	for _, ps := range sch {
		if len(ps) == 0 || ps[0].start != 0 {
			return false
		}
		for i, p := range ps {
			if p.price < 0 {
				return false
			}
			if i > 0 && p.start <= ps[i-1].start {
				return false
			}
		}
		last := ps[len(ps)-1]
		if last.start >= 86400 {
			return false
		}
	}
	return true
}

func (m *naiveModel) registerVersion(at time.Time, sch [3][]naivePeriod) string {
	if at.IsZero() || at.Nanosecond() != 0 || !validNaiveSchedules(sch) {
		return "参数非法"
	}
	for pt := range m.points {
		if end, ok := m.maxClosedEnd(pt); ok && !at.After(end) {
			return "月份已封账"
		}
	}
	for _, v := range m.versions {
		if v.at.Equal(at) {
			return "参数非法"
		}
	}
	m.versions = append(m.versions, naiveVersion{at: at, sch: sch})
	sort.Slice(m.versions, func(i, j int) bool { return m.versions[i].at.Before(m.versions[j].at) })
	return ""
}

func (m *naiveModel) setHoliday(date string, on bool) string {
	t, err := time.ParseInLocation("2006-01-02", date, m.loc)
	if err != nil || t.Nanosecond() != 0 {
		return "参数非法"
	}
	for pt := range m.points {
		if end, ok := m.maxClosedEnd(pt); ok && t.Before(end) {
			return "月份已封账"
		}
	}
	if on {
		m.holidays[date] = true
	} else {
		delete(m.holidays, date)
	}
	return ""
}

func (m *naiveModel) dayType(t time.Time) int {
	if m.holidays[t.Format("2006-01-02")] {
		return 2
	}
	if w := t.Weekday(); w == time.Saturday || w == time.Sunday {
		return 1
	}
	return 0
}

func (m *naiveModel) activeVersion(t time.Time) (naiveVersion, bool) {
	var best *naiveVersion
	for i := range m.versions {
		if !m.versions[i].at.After(t) {
			best = &m.versions[i]
		}
	}
	if best == nil {
		return naiveVersion{}, false
	}
	return *best, true
}

func (m *naiveModel) registerReading(pt string, at time.Time, wh int64) string {
	if pt == "" || at.IsZero() || at.Nanosecond() != 0 || wh < 0 {
		return "参数非法"
	}
	if end, ok := m.maxClosedEnd(pt); ok && !at.After(end) {
		return "月份已封账"
	}
	rs := m.points[pt]
	if len(rs) > 0 && !at.After(rs[len(rs)-1].at) {
		return "时序错误"
	}
	if len(rs) > 0 && wh < rs[len(rs)-1].wh {
		return "读数倒退"
	}
	m.points[pt] = append(rs, naiveReading{at: at, wh: wh})
	return ""
}

func (m *naiveModel) correctReading(pt string, at time.Time, wh int64) string {
	if pt == "" || at.IsZero() || at.Nanosecond() != 0 || wh < 0 {
		return "参数非法"
	}
	if end, ok := m.maxClosedEnd(pt); ok && !at.After(end) {
		return "月份已封账"
	}
	rs := m.points[pt]
	idx := -1
	for i := range rs {
		if rs[i].at.Equal(at) {
			idx = i
		}
	}
	if idx < 0 {
		return "参数非法"
	}
	if idx > 0 && wh < rs[idx-1].wh {
		return "读数倒退"
	}
	if idx+1 < len(rs) && wh > rs[idx+1].wh {
		return "读数倒退"
	}
	rs[idx].wh = wh
	m.points[pt] = rs
	return ""
}

func (m *naiveModel) deleteReading(pt string, at time.Time) string {
	if pt == "" || at.IsZero() || at.Nanosecond() != 0 {
		return "参数非法"
	}
	if end, ok := m.maxClosedEnd(pt); ok && !at.After(end) {
		return "月份已封账"
	}
	rs := m.points[pt]
	if len(rs) == 0 || !rs[len(rs)-1].at.Equal(at) {
		return "时序错误"
	}
	m.points[pt] = rs[:len(rs)-1]
	return ""
}

// cutGap 暴力切片：每一步用线性扫描求当前版本、日类型与时段，
// 下一边界 = min(次日 0 点, 时段结束, 下一个版本生效时刻)。
func (m *naiveModel) cutGap(lo, hi time.Time, totalWh int64) []naiveSeg {
	var segs []naiveSeg
	cur := lo
	for cur.Before(hi) {
		seg := naiveSeg{lo: cur}
		y, mo, d := cur.Date()
		midnight := time.Date(y, mo, d, 0, 0, 0, 0, m.loc).AddDate(0, 0, 1)
		monthEnd := time.Date(y, mo, 1, 0, 0, 0, 0, m.loc).AddDate(0, 1, 0)
		seg.day = m.dayType(cur)
		end := midnight
		if monthEnd.Before(end) {
			end = monthEnd
		}
		v, ok := m.activeVersion(cur)
		if !ok {
			seg.billable = false
		} else {
			ps := v.sch[seg.day]
			sec := cur.Hour()*3600 + cur.Minute()*60 + cur.Second()
			idx := 0
			for i := range ps {
				if ps[i].start <= sec {
					idx = i
				}
			}
			seg.startSec = ps[idx].start
			seg.price = ps[idx].price
			seg.billable = true
			pEnd := time.Date(y, mo, d, 0, 0, 0, 0, m.loc).Add(time.Duration(periodEndNaive(ps, idx)) * time.Second)
			if pEnd.Before(end) {
				end = pEnd
			}
		}
		for _, nv := range m.versions {
			if nv.at.After(cur) && nv.at.Before(end) {
				end = nv.at
			}
		}
		if end.After(hi) {
			end = hi
		}
		seg.hi = end
		segs = append(segs, seg)
		cur = end
	}
	// 电量与金额由 compute 分摊；这里仅返回时间片。
	return segs
}

func periodEndNaive(ps []naivePeriod, idx int) int {
	if idx+1 < len(ps) {
		return ps[idx+1].start
	}
	return 86400
}

type naiveFullSeg struct {
	naiveSeg
	month  int64
	wh     int64
	amount int64
}

func (m *naiveModel) gapFull(lo, hi time.Time, totalWh int64) []naiveFullSeg {
	segs := m.cutGap(lo, hi, totalWh)
	totalDur := int64(hi.Sub(lo) / time.Second)
	out := make([]naiveFullSeg, len(segs))
	assigned := int64(0)
	for i, s := range segs {
		dur := int64(s.hi.Sub(s.lo) / time.Second)
		wh := totalWh * dur / totalDur
		if i == len(segs)-1 {
			wh = totalWh - assigned
		}
		assigned += wh
		amount := int64(0)
		if s.billable {
			amount = wh * s.price / 1000
		}
		out[i] = naiveFullSeg{naiveSeg: s, month: naiveKey(s.lo), wh: wh, amount: amount}
	}
	return out
}

func (m *naiveModel) bill(pt string, month time.Time) (naiveBill, bool) {
	k := naiveKey(month)
	if s, ok := m.snaps[pt][k]; ok {
		return s, true
	}
	b := naiveBill{}
	type ekey struct {
		day, start int
		price      int64
	}
	buckets := map[ekey]*naiveEntry{}
	rs := m.points[pt]
	for i := 0; i+1 < len(rs); i++ {
		for _, s := range m.gapFull(rs[i].at, rs[i+1].at, rs[i+1].wh-rs[i].wh) {
			if s.month != k {
				continue
			}
			b.totalWh += s.wh
			if !s.billable {
				b.unbill += s.wh
				continue
			}
			key := ekey{s.day, s.startSec, s.price}
			e := buckets[key]
			if e == nil {
				e = &naiveEntry{day: s.day, startSec: s.startSec, price: s.price}
				buckets[key] = e
			}
			e.wh += s.wh
			e.amount += s.amount
		}
	}
	for _, e := range buckets {
		if e.wh == 0 && e.amount == 0 {
			continue
		}
		b.entries = append(b.entries, *e)
		b.amount += e.amount
	}
	sort.Slice(b.entries, func(i, j int) bool {
		if b.entries[i].day != b.entries[j].day {
			return b.entries[i].day < b.entries[j].day
		}
		if b.entries[i].startSec != b.entries[j].startSec {
			return b.entries[i].startSec < b.entries[j].startSec
		}
		return b.entries[i].price < b.entries[j].price
	})
	return b, false
}

func (m *naiveModel) closeMonth(pt string, month time.Time) string {
	k := naiveKey(month)
	if m.closed[pt][k] {
		return ""
	}
	b, _ := m.bill(pt, month)
	if b.unbill > 0 {
		return "不可计价片阻止封账"
	}
	y, mo := int(k/12), time.Month(k%12+1)
	end := time.Date(y, mo, 1, 0, 0, 0, 0, m.loc).AddDate(0, 1, 0)
	rs := m.points[pt]
	if len(rs) == 0 || rs[len(rs)-1].at.Before(end) {
		return "读数不足"
	}
	if m.closed[pt] == nil {
		m.closed[pt] = map[int64]bool{}
		m.snaps[pt] = map[int64]naiveBill{}
	}
	m.closed[pt][k] = true
	m.snaps[pt][k] = b
	return ""
}
