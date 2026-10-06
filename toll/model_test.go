package toll

// 独立编写的朴素全量重算模型:每次操作后从原始记录整体重算
// (DFS 枚举全部简单路径),不复用 Service 的任何增量状态。

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"
)

type mJourney struct {
	id       string
	vid      string
	entry    GantryRecord
	exit     GantryRecord
	hasExit  bool
	records  []GantryRecord
	settled  bool
	closed   bool
	path     []string
	fee      int64
	received int64
	charges  int64
	refunds  int64
	capped   int64
}

type mVehicle struct {
	typ0     string
	changes  []TypeChange
	kept     map[string][]time.Time
	journeys []*mJourney
}

type model struct {
	net      *Network
	cfg      Config
	vehicles map[string]*mVehicle
	ledgers  map[MonthKey]*MonthLedger
	seq      int
}

func newModel(net *Network, cfg Config) *model {
	return &model{
		net:      net,
		cfg:      cfg,
		vehicles: make(map[string]*mVehicle),
		ledgers:  make(map[MonthKey]*MonthLedger),
	}
}

func (m *model) typeAt(v *mVehicle, t time.Time) string {
	typ := v.typ0
	for _, c := range v.changes {
		if c.Effective.After(t) {
			break
		}
		typ = c.Type
	}
	return typ
}

func (m *model) registerVehicle(id, typ string) {
	m.vehicles[id] = &mVehicle{typ0: typ, kept: make(map[string][]time.Time)}
}

func (m *model) changeType(id, typ string, eff time.Time) {
	m.seq++
	v := m.vehicles[id]
	v.changes = append(v.changes, TypeChange{Effective: eff, Type: typ, Seq: m.seq})
	sort.SliceStable(v.changes, func(i, j int) bool {
		a, b := v.changes[i], v.changes[j]
		if !a.Effective.Equal(b.Effective) {
			return a.Effective.Before(b.Effective)
		}
		return a.Seq < b.Seq
	})
}

func (m *model) entry(id, gantry string, t time.Time) string {
	m.seq++
	v := m.vehicles[id]
	j := &mJourney{
		id:    fmt.Sprintf("%s#%d", id, len(v.journeys)+1),
		vid:   id,
		entry: GantryRecord{Gantry: gantry, Time: t, Seq: m.seq, Status: StatusValid},
	}
	v.journeys = append(v.journeys, j)
	v.kept[gantry] = append(v.kept[gantry], t)
	return j.id
}

func (m *model) openJourney(v *mVehicle) *mJourney {
	if n := len(v.journeys); n > 0 {
		if j := v.journeys[n-1]; !j.settled && !j.closed {
			return j
		}
	}
	return nil
}

// dfsBest 枚举 from 到 to 的全部简单路径,取费用最低、并列字典序最小者。
func (m *model) dfsBest(from, to, typ string) ([]string, int64, bool) {
	if from == to {
		return []string{from}, 0, true
	}
	var bestPath []string
	var bestCost int64
	found := false
	visited := map[string]bool{from: true}
	var dfs func(node string, cost int64, path []string)
	dfs = func(node string, cost int64, path []string) {
		if node == to {
			if !found || cost < bestCost || (cost == bestCost && lessPath(path, bestPath)) {
				found = true
				bestCost = cost
				bestPath = append([]string(nil), path...)
			}
			return
		}
		for _, seg := range m.net.adj[node] {
			if visited[seg.To] {
				continue
			}
			visited[seg.To] = true
			dfs(seg.To, cost+seg.Distance*seg.Rates[typ], append(path, seg.To))
			visited[seg.To] = false
		}
	}
	dfs(from, 0, []string{from})
	return bestPath, bestCost, found
}

// recomputeJourney 从原始记录整体重算行程路径与费用。
func (m *model) recomputeJourney(v *mVehicle, j *mJourney) bool {
	wps := []GantryRecord{j.entry}
	recs := make([]GantryRecord, 0, len(j.records))
	for _, r := range j.records {
		if r.Status != StatusValid {
			continue
		}
		if r.Time.Before(j.entry.Time) || r.Time.After(j.exit.Time) {
			continue
		}
		recs = append(recs, r)
	}
	sort.SliceStable(recs, func(a, b int) bool {
		if !recs[a].Time.Equal(recs[b].Time) {
			return recs[a].Time.Before(recs[b].Time)
		}
		return recs[a].Seq < recs[b].Seq
	})
	wps = append(wps, recs...)
	wps = append(wps, j.exit)
	var full []string
	var total int64
	for i := 0; i+1 < len(wps); i++ {
		typ := m.typeAt(v, wps[i].Time)
		p, c, ok := m.dfsBest(wps[i].Gantry, wps[i+1].Gantry, typ)
		if !ok {
			return false
		}
		if i == 0 {
			full = append(full, p...)
		} else {
			full = append(full, p[1:]...)
		}
		total += c
	}
	j.path = full
	j.fee = total
	return true
}

func (m *model) ledger(id string, t time.Time) *MonthLedger {
	lt := t.In(m.cfg.Location)
	key := MonthKey{VehicleID: id, Year: lt.Year(), Month: lt.Month()}
	l, ok := m.ledgers[key]
	if !ok {
		l = &MonthLedger{}
		m.ledgers[key] = l
	}
	return l
}

// applyDelta 朴素地按差额落下收取或退款,规则与规格一致。
func (m *model) applyDelta(j *mJourney) {
	delta := j.fee - j.received
	if delta == 0 {
		return
	}
	l := m.ledger(j.vid, j.exit.Time)
	if delta > 0 {
		charge := delta
		if m.cfg.MonthlyCap > 0 {
			remain := m.cfg.MonthlyCap - l.Received
			if remain < 0 {
				remain = 0
			}
			if charge > remain {
				charge = remain
			}
		}
		j.received += charge
		j.charges += charge
		j.capped += delta - charge
		l.Received += charge
		l.CappedUncollected += delta - charge
	} else {
		refund := -delta
		if refund > l.Received {
			l.RefundOverflow += refund - l.Received
			refund = l.Received
		}
		j.received -= refund
		j.refunds += refund
		l.Received -= refund
	}
}

// record 朴素判定记录归属并返回状态,迟到记录触发整体重算。
func (m *model) record(id, gantry string, t, now time.Time) RecordStatus {
	m.seq++
	v := m.vehicles[id]
	rec := GantryRecord{Gantry: gantry, Time: t, Seq: m.seq}
	for _, kt := range v.kept[gantry] {
		d := t.Sub(kt)
		if d < 0 {
			d = -d
		}
		if d < m.cfg.DuplicateWindow {
			return StatusDuplicate
		}
	}
	v.kept[gantry] = append(v.kept[gantry], t)
	for i := len(v.journeys) - 1; i >= 0; i-- {
		j := v.journeys[i]
		if !j.settled || t.Before(j.entry.Time) || t.After(j.exit.Time) {
			continue
		}
		if now.Sub(j.exit.Time) > m.cfg.BackchargeWindow {
			rec.Status = StatusExpired
			j.records = append(j.records, rec)
			return StatusExpired
		}
		rec.Status = StatusValid
		j.records = append(j.records, rec)
		if m.recomputeJourney(v, j) {
			m.applyDelta(j)
		}
		return StatusLate
	}
	if j := m.openJourney(v); j != nil && !t.Before(j.entry.Time) {
		rec.Status = StatusValid
		j.records = append(j.records, rec)
		return StatusAccepted
	}
	return StatusOrphan
}

// exit 结算:整体推定路径与费用,不可达则行程保持未结算。
func (m *model) exit(id, gantry string, t time.Time) bool {
	m.seq++
	v := m.vehicles[id]
	j := m.openJourney(v)
	if j == nil {
		return false
	}
	j.exit = GantryRecord{Gantry: gantry, Time: t, Seq: m.seq, Status: StatusValid}
	j.hasExit = true
	if !m.recomputeJourney(v, j) {
		j.hasExit = false
		return false
	}
	for i := range j.records {
		if j.records[i].Status == StatusValid && j.records[i].Time.After(t) {
			j.records[i].Status = StatusOrphan
		}
	}
	j.settled = true
	v.kept[gantry] = append(v.kept[gantry], t)
	m.applyDelta(j)
	return true
}

func (m *model) close(id string) {
	v := m.vehicles[id]
	if j := m.openJourney(v); j != nil {
		j.closed = true
	}
}

// compare 全量对照 Service 与模型的每个行程与每个月台账。
func compare(t *testing.T, svc *Service, m *model, ctx string) {
	t.Helper()
	for vid, mv := range m.vehicles {
		for _, mj := range mv.journeys {
			view, err := svc.QueryJourney(mj.id)
			if err != nil {
				t.Fatalf("%s: query %s: %v", ctx, mj.id, err)
			}
			if view.Settled != mj.settled || view.Closed != mj.closed {
				t.Fatalf("%s: %s settled/closed = %v/%v, model %v/%v",
					ctx, mj.id, view.Settled, view.Closed, mj.settled, mj.closed)
			}
			if mj.settled {
				if !reflect.DeepEqual(view.Path, mj.path) {
					t.Fatalf("%s: %s path = %v, model %v", ctx, mj.id, view.Path, mj.path)
				}
				if view.Fee != mj.fee || view.Received != mj.received {
					t.Fatalf("%s: %s fee/received = %d/%d, model %d/%d",
						ctx, mj.id, view.Fee, view.Received, mj.fee, mj.received)
				}
				var charges, refunds, capped int64
				for _, a := range view.Adjustments {
					if a.Kind == AdjustRefund {
						refunds += a.Amount
					} else {
						charges += a.Amount
					}
					capped += a.CappedUncollected
				}
				if charges != mj.charges || refunds != mj.refunds || capped != mj.capped {
					t.Fatalf("%s: %s adj sums = %d/%d/%d, model %d/%d/%d",
						ctx, mj.id, charges, refunds, capped, mj.charges, mj.refunds, mj.capped)
				}
				// 不变式:实收 = 累计收取 - 累计退款,且不超过费用。
				if view.Received != charges-refunds || view.Received > view.Fee {
					t.Fatalf("%s: %s invariant broken: %+v", ctx, mj.id, view)
				}
			}
		}
		for key, ml := range m.ledgers {
			if key.VehicleID != vid {
				continue
			}
			sl, err := svc.QueryMonth(vid, time.Date(key.Year, key.Month, 1, 0, 0, 0, 0, m.cfg.Location))
			if err != nil {
				t.Fatalf("%s: query month: %v", ctx, err)
			}
			if sl != *ml {
				t.Fatalf("%s: ledger %v = %+v, model %+v", ctx, key, sl, *ml)
			}
			if ml.Received < 0 || (m.cfg.MonthlyCap > 0 && ml.Received > m.cfg.MonthlyCap) {
				t.Fatalf("%s: ledger %v out of bounds: %+v", ctx, key, *ml)
			}
		}
	}
}

// randomDAG 构造一个有向无环路网。
func randomDAG() *Network {
	net := NewNetwork()
	edges := [][2]string{
		{"A", "B"}, {"B", "C"}, {"C", "D"}, {"D", "E"}, {"E", "F"},
		{"A", "C"}, {"B", "D"}, {"C", "E"}, {"D", "F"}, {"A", "D"},
		{"B", "E"}, {"C", "F"},
	}
	r := rand.New(rand.NewSource(42))
	for _, e := range edges {
		d := int64(1 + r.Intn(9))
		rates := map[string]int64{"car": int64(1 + r.Intn(3)), "truck": int64(2 + r.Intn(5))}
		if err := net.AddSegment(e[0], e[1], d, rates); err != nil {
			panic(err)
		}
	}
	return net
}

// 随机操作序列:Service 与朴素全量重算模型逐步对照,
// 日志打印每条操作的输入、输出与判定依据。
func TestRandomOpsAgainstNaiveModel(t *testing.T) {
	for _, seed := range []int64{1, 7, 23, 99, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomOps(t, seed)
		})
	}
}

func runRandomOps(t *testing.T, seed int64) {
	net := randomDAG()
	cfg := Config{
		DuplicateWindow:  15 * time.Second,
		BackchargeWindow: 1800 * time.Second,
		MonthlyCap:       200,
		Location:         time.UTC,
	}
	svc := NewService(net, cfg)
	m := newModel(net, cfg)
	r := rand.New(rand.NewSource(seed))
	now := base
	vehicles := []string{"V0", "V1", "V2"}
	types := []string{"car", "truck"}
	gantries := []string{"A", "B", "C", "D", "E", "F"}
	gidx := map[string]int{"A": 0, "B": 1, "C": 2, "D": 3, "E": 4, "F": 5}
	type settledInfo struct {
		entry, exit   time.Time
		entryG, exitG string
	}
	lastSettled := map[string]settledInfo{}
	// 在 [lo, hi] 字母区间内随机取门架,保证有向可达。
	between := func(lo, hi string) string {
		a, b := gidx[lo], gidx[hi]
		if a > b {
			a, b = b, a
		}
		return gantries[a+r.Intn(b-a+1)]
	}
	for i, v := range vehicles {
		if err := svc.RegisterVehicle(now, v, types[i%2]); err != nil {
			t.Fatal(err)
		}
		m.registerVehicle(v, types[i%2])
		now = now.Add(time.Second)
	}
	for op := 0; op < 500; op++ {
		now = now.Add(time.Duration(1+r.Intn(90)) * time.Second)
		vid := vehicles[r.Intn(len(vehicles))]
		mv := m.vehicles[vid]
		open := m.openJourney(mv)
		choice := r.Intn(100)
		ctx := fmt.Sprintf("op %d", op)
		switch {
		case open == nil && choice < 55:
			// 入口登记(避开无出边的 F)。
			g := gantries[r.Intn(len(gantries)-1)]
			ts := now.Add(-time.Duration(r.Intn(30)) * time.Second)
			jid, err := svc.Entry(now, vid, g, ts)
			t.Logf("%s ENTRY %s %s@%s -> id=%v err=%v", ctx, vid, g, ts, jid, err)
			if err != nil {
				t.Fatalf("%s: entry: %v", ctx, err)
			}
			if mid := m.entry(vid, g, ts); mid != jid {
				t.Fatalf("%s: journey id %s != model %s", ctx, jid, mid)
			}
		case open == nil:
			// 无未结算行程时的车型变更。
			typ := types[r.Intn(len(types))]
			eff := now.Add(-time.Duration(r.Intn(1200)) * time.Second)
			err := svc.ChangeVehicleType(now, vid, typ, eff)
			t.Logf("%s TYPE %s -> %s @%s err=%v", ctx, vid, typ, eff, err)
			if err != nil {
				t.Fatalf("%s: type change: %v", ctx, err)
			}
			m.changeType(vid, typ, eff)
		case choice < 40:
			// 行程内中间记录(时刻在入口之后,门架在入口下游)。
			g := between(open.entry.Gantry, "F")
			ts := open.entry.Time.Add(time.Duration(r.Intn(400)) * time.Second)
			rr, err := svc.Record(now, vid, g, ts)
			if err != nil {
				t.Fatalf("%s: record: %v", ctx, err)
			}
			ms := m.record(vid, g, ts, now)
			t.Logf("%s RECORD %s %s@%s -> %s (model %s)", ctx, vid, g, ts, rr.Status, ms)
			if rr.Status != ms {
				t.Fatalf("%s: status %s != model %s", ctx, rr.Status, ms)
			}
		case choice < 50:
			// 疑似重复记录:同门架、时刻接近入口。
			ts := open.entry.Time.Add(time.Duration(r.Intn(20)) * time.Second)
			rr, err := svc.Record(now, vid, open.entry.Gantry, ts)
			if err != nil {
				t.Fatalf("%s: dup record: %v", ctx, err)
			}
			ms := m.record(vid, open.entry.Gantry, ts, now)
			t.Logf("%s DUP? %s %s@%s -> %s (model %s)", ctx, vid, open.entry.Gantry, ts, rr.Status, ms)
			if rr.Status != ms {
				t.Fatalf("%s: dup status %s != model %s", ctx, rr.Status, ms)
			}
		case choice < 65:
			// 出口结算;保证出口时刻不早于入口且不晚于操作时刻。
			g := between(open.entry.Gantry, "F")
			ts := open.entry.Time.Add(time.Duration(60+r.Intn(600)) * time.Second)
			if ts.After(now) {
				now = ts
			}
			res, err := svc.Exit(now, vid, g, ts)
			t.Logf("%s EXIT %s %s@%s -> %+v err=%v", ctx, vid, g, ts, res, err)
			if err != nil {
				if !errors.Is(err, ErrPathUnreachable) {
					t.Fatalf("%s: exit: %v", ctx, err)
				}
				break // 路径不可达:双方均不改变状态
			}
			if !m.exit(vid, g, ts) {
				t.Fatalf("%s: model exit failed but service succeeded", ctx)
			}
			lastSettled[vid] = settledInfo{entry: open.entry.Time, exit: ts, entryG: open.entry.Gantry, exitG: g}
		case choice < 80:
			// 迟到记录:时刻落在上一已结算行程的入口与出口之间。
			ls, ok := lastSettled[vid]
			if !ok {
				continue
			}
			// 40% 概率先做一次生效于行程前的车型变更(偏向更便宜的 car),以产生退款。
			if r.Intn(10) < 4 {
				typ := "car"
				if r.Intn(4) == 0 {
					typ = "truck"
				}
				eff := ls.entry.Add(-time.Second)
				if err := svc.ChangeVehicleType(now, vid, typ, eff); err != nil {
					t.Fatalf("%s: type change: %v", ctx, err)
				}
				m.changeType(vid, typ, eff)
				t.Logf("%s TYPE %s -> %s @%s (before late record)", ctx, vid, typ, eff)
			}
			g := between(ls.entryG, ls.exitG)
			span := int(ls.exit.Sub(ls.entry).Seconds())
			ts := ls.entry.Add(time.Duration(r.Intn(span+1)) * time.Second)
			rr, err := svc.Record(now, vid, g, ts)
			if err != nil {
				t.Fatalf("%s: late record: %v", ctx, err)
			}
			ms := m.record(vid, g, ts, now)
			adj := "none"
			if rr.Adjustment != nil {
				adj = fmt.Sprintf("%s %d (%s)", rr.Adjustment.Kind, rr.Adjustment.Amount, rr.Adjustment.Reason)
			}
			t.Logf("%s LATE %s %s@%s -> %s adj=%s (model %s)", ctx, vid, g, ts, rr.Status, adj, ms)
			if rr.Status != ms {
				t.Fatalf("%s: late status %s != model %s", ctx, rr.Status, ms)
			}
		case choice < 90:
			// 行程中的车型变更,生效时刻在行程附近。
			typ := types[r.Intn(len(types))]
			eff := open.entry.Time.Add(time.Duration(r.Intn(600)-100) * time.Second)
			err := svc.ChangeVehicleType(now, vid, typ, eff)
			t.Logf("%s TYPE %s -> %s @%s err=%v", ctx, vid, typ, eff, err)
			if err != nil {
				t.Fatalf("%s: type change: %v", ctx, err)
			}
			m.changeType(vid, typ, eff)
		case choice < 95:
			// 孤立记录:时刻在入口之前。
			g := gantries[r.Intn(len(gantries))]
			ts := open.entry.Time.Add(-time.Duration(1+r.Intn(300)) * time.Second)
			rr, err := svc.Record(now, vid, g, ts)
			if err != nil {
				t.Fatalf("%s: orphan record: %v", ctx, err)
			}
			ms := m.record(vid, g, ts, now)
			t.Logf("%s ORPHAN %s %s@%s -> %s (model %s)", ctx, vid, g, ts, rr.Status, ms)
			if rr.Status != ms {
				t.Fatalf("%s: orphan status %s != model %s", ctx, rr.Status, ms)
			}
		default:
			// 人工关闭未结算行程。
			err := svc.CloseJourney(now, vid)
			t.Logf("%s CLOSE %s -> err=%v", ctx, vid, err)
			if err == nil {
				m.close(vid)
			}
		}
		compare(t, svc, m, ctx)
	}
	t.Logf("random walk finished: %d journeys", len(m.vehicles["V0"].journeys)+
		len(m.vehicles["V1"].journeys)+len(m.vehicles["V2"].journeys))
}
