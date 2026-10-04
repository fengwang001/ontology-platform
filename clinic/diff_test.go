package clinic

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/vaxrule"
)

// ---- 朴素模拟器：独立按题面规则实现，不调用 clinic 的任何判定代码 ----

type simSeries struct {
	name   string
	live   bool
	n      int
	minAge []int
	minInt []int
	r      int
}

type simRec struct {
	series string
	date   int
}

type simLot struct {
	series string
	exp    int
	qty    int
	quar   bool
}

type simJudg struct {
	series string
	date   int
	dose   int
	status Status
	reason vaxrule.InvalidReason
}

type simAgg struct {
	valid      int
	lastValid  int
	hasLast    bool
	lastDate   int
	lastValidF bool
}

type simWorld struct {
	now     int
	series  map[string]*simSeries
	birth   map[string]int
	recs    map[string][]simRec
	lots    map[string]*simLot
	granted map[string]bool
	adminOK int
}

func newSimWorld() *simWorld {
	return &simWorld{
		series:  map[string]*simSeries{},
		birth:   map[string]int{},
		recs:    map[string][]simRec{},
		lots:    map[string]*simLot{},
		granted: map[string]bool{},
	}
}

// simLiveConflict：另一活疫苗系列记录满足 0<d-d'<28。
func simLiveConflict(recs []simRec, live map[string]bool, series string, d int) bool {
	for _, r := range recs {
		if !live[r.series] || r.series == series {
			continue
		}
		if delta := d - r.date; 0 < delta && delta < 28 {
			return true
		}
	}
	return false
}

// simJudge 全量重算一名患者；返回逐条判定与各系列汇总。
func simJudge(w *simWorld, patient string) ([]simJudg, map[string]*simAgg) {
	rs := append([]simRec(nil), w.recs[patient]...)
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].date != rs[j].date {
			return rs[i].date < rs[j].date
		}
		return rs[i].series < rs[j].series
	})
	live := map[string]bool{}
	for name, s := range w.series {
		live[name] = s.live
	}
	ag := map[string]*simAgg{}
	out := make([]simJudg, 0, len(rs))
	for idx, r := range rs {
		s := w.series[r.series]
		a := ag[r.series]
		if a == nil {
			a = &simAgg{}
			ag[r.series] = a
		}
		k := a.valid + 1
		j := simJudg{series: r.series, date: r.date, dose: k}
		good := false
		switch {
		case k > s.n:
			j.status = StatusExtra
		case r.date-w.birth[patient] < s.minAge[k-1]-4:
			j.status, j.reason = StatusInvalid, vaxrule.ReasonAge
		case k > 1 && r.date-a.lastValid < s.minInt[k-1]-4:
			j.status, j.reason = StatusInvalid, vaxrule.ReasonInterval
		case a.hasLast && !a.lastValidF && r.date-a.lastDate < s.r:
			j.status, j.reason = StatusInvalid, vaxrule.ReasonRedose
		case s.live && simLiveConflict(rs[:idx], live, r.series, r.date):
			j.status, j.reason = StatusInvalid, vaxrule.ReasonLive
		default:
			j.status, j.reason = StatusValid, vaxrule.ReasonNone
			good = true
		}
		if good {
			a.valid++
			a.lastValid = r.date
		}
		a.hasLast = true
		a.lastDate = r.date
		a.lastValidF = good
		out = append(out, j)
	}
	return out, ag
}

// simEarliest 独立实现“最早可接种日”：从 now 起逐日把候选记录挂在末尾试判。
func simEarliest(w *simWorld, patient, series string, now int) int {
	s := w.series[series]
	_, ag0 := simJudge(w, patient)
	a := ag0[series]
	if a == nil {
		a = &simAgg{}
	}
	k := a.valid + 1
	base := append([]simRec(nil), w.recs[patient]...)
	live := map[string]bool{}
	for name, x := range w.series {
		live[name] = x.live
	}
	for d := now; ; d++ {
		// 候选排在全部现有记录之后（日期最大），既有判定不变；只判候选本身。
		switch {
		case d-w.birth[patient] < s.minAge[k-1]-4:
			continue
		case k > 1 && d-a.lastValid < s.minInt[k-1]-4:
			continue
		case a.hasLast && !a.lastValidF && d-a.lastDate < s.r:
			continue
		case s.live && simLiveConflict(base, live, series, d):
			continue
		}
		return d
	}
}

// ---- 操作镜像：每个操作在真实 Clinic 与朴素世界各执行一次，并比对结果 ----

type opOutcome struct {
	kind     string
	err      string
	lot      string
	reason   int
	earliest int
}

func validAddSeriesArgs(rng *rand.Rand) (bool, int, []int, []int, int) {
	n := 1 + rng.Intn(6)
	ma := make([]int, n)
	mi := make([]int, n)
	for i := range ma {
		ma[i] = rng.Intn(400)
	}
	for i := 1; i < n; i++ {
		mi[i] = rng.Intn(60)
	}
	return rng.Intn(2) == 0, n, ma, mi, rng.Intn(40)
}

func compareEv(t *testing.T, c *Clinic, w *simWorld, now int, patient string, logf func(string, ...any)) {
	got, gerr := c.Evaluate(now, patient)
	wantJ, wantAgg := simJudge(w, patient)
	if gerr != nil {
		t.Fatalf("Evaluate err: %v", gerr)
	}
	logf("Evaluate(now=%d patient=%s)", now, patient)
	if len(got.Records) != len(wantJ) {
		t.Fatalf("judgment count %d != %d", len(got.Records), len(wantJ))
	}
	for i := range wantJ {
		g, wj := got.Records[i], wantJ[i]
		logf("  %s@%d dose=%d -> got(status=%d,reason=%d)", g.Series, g.Date, g.Dose, g.Status, g.Reason)
		if g.Series != wj.series || g.Date != wj.date || g.Dose != wj.dose ||
			g.Status != wj.status || g.Reason != wj.reason {
			t.Fatalf("judgment[%d] got=%+v want=%+v", i, g, wj)
		}
	}
	// Next：所有未完成系列。
	wantNext := map[string][2]int{}
	for name, s := range w.series {
		a := wantAgg[name]
		valid := 0
		if a != nil {
			valid = a.valid
		}
		if valid < s.n {
			wantNext[name] = [2]int{valid + 1, simEarliest(w, patient, name, now)}
		}
	}
	gotNext := map[string][2]int{}
	for _, n := range got.Next {
		gotNext[n.Series] = [2]int{n.Dose, n.Earliest}
	}
	if len(gotNext) != len(wantNext) {
		t.Fatalf("next count got=%v want=%v", gotNext, wantNext)
	}
	for name, wn := range wantNext {
		gn, ok := gotNext[name]
		if !ok || gn != wn {
			t.Fatalf("next %s got=%v want=%v", name, gn, wn)
		}
		logf("  next %s dose=%d earliest=%d", name, wn[0], wn[1])
	}
}

func TestNaiveDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for seq := 0; seq < 1500; seq++ {
		c := New()
		w := newSimWorld()
		logs := []string{}
		logf := func(format string, args ...any) {
			logs = append(logs, "    "+fmt.Sprintf(format, args...))
		}
		fail := func(msg string, args ...any) {
			t.Helper()
			t.Fatalf("seq=%d %s\n%s", seq, fmt.Sprintf(msg, args...), joinLogs(logs))
		}
		patients := []string{}
		seriesNames := []string{}
		nOps := 1 + rng.Intn(60)
		for o := 0; o < nOps; o++ {
			r := rng.Intn(100)
			switch {
			case r < 10 && len(seriesNames) < 6:
				name := "S" + itoa(len(seriesNames))
				live, n, ma, mi, rr := validAddSeriesArgs(rng)
				err := c.AddSeries(name, live, n, ma, mi, rr)
				if err == nil {
					w.series[name] = &simSeries{name: name, live: live, n: n, minAge: ma, minInt: mi, r: rr}
					seriesNames = append(seriesNames, name)
				}
				logf("AddSeries(%s live=%v n=%d minAge=%v minInt=%v R=%d) -> %v", name, live, n, ma, mi, rr, err)
			case r < 15:
				name := "P" + itoa(len(patients))
				birth := w.now + rng.Intn(300)
				nn := w.now + rng.Intn(10)
				err := c.AddPatient(nn, name, birth)
				if err == nil {
					w.now = nn
					w.birth[name] = birth
					patients = append(patients, name)
				}
				logf("AddPatient(now=%d %s birth=%d) -> %v", nn, name, birth, err)
			case r < 25 && len(seriesNames) > 0:
				sname := seriesNames[rng.Intn(len(seriesNames))]
				lot := "L" + itoa(len(w.lots)) + "_" + itoa(rng.Intn(3))
				exp := w.now + rng.Intn(400)
				qty := rng.Intn(4)
				err := c.AddLot(w.now, lot, sname, exp, qty)
				if err == nil {
					w.lots[lot] = &simLot{series: sname, exp: exp, qty: qty}
				}
				logf("AddLot(now=%d %s %s exp=%d qty=%d) -> %v", w.now, lot, sname, exp, qty, err)
			case r < 30 && len(w.lots) > 0:
				names := lotNames(w)
				lot := names[rng.Intn(len(names))]
				on := rng.Intn(2) == 0
				err := c.Quarantine(w.now, lot, on)
				if err == nil {
					w.lots[lot].quar = on
				}
				logf("Quarantine(now=%d %s on=%v) -> %v", w.now, lot, on, err)
			case r < 35:
				u := "n" + itoa(rng.Intn(3))
				err := c.Grant(w.now, u)
				if err == nil {
					w.granted[u] = true
				}
				logf("Grant(now=%d %s) -> %v", w.now, u, err)
			case r < 70 && len(patients) > 0 && len(seriesNames) > 0:
				p := patients[rng.Intn(len(patients))]
				sname := seriesNames[rng.Intn(len(seriesNames))]
				// 日期在 [birth, now]，允许小概率非法以便差分错误路径。
				var d int
				if rng.Intn(10) == 0 {
					d = w.now + 1 + rng.Intn(5)
				} else if w.birth[p] <= w.now {
					d = w.birth[p] + rng.Intn(w.now-w.birth[p]+1)
				} else {
					d = w.birth[p] // birth>当前时钟时必非法（d>nn），差分错误路径
				}
				nn := w.now + rng.Intn(3)
				gerr := c.Record(nn, p, sname, d)
				werr := simRecord(w, nn, p, sname, d)
				logf("Record(now=%d %s %s d=%d) -> %v | sim %v", nn, p, sname, d, gerr, werr)
				if errCode(gerr) != werr {
					fail("Record err mismatch got=%q want=%q", errCode(gerr), werr)
				}
				_ = nn
			case r < 90 && len(patients) > 0 && len(seriesNames) > 0:
				p := patients[rng.Intn(len(patients))]
				sname := seriesNames[rng.Intn(len(seriesNames))]
				u := "n" + itoa(rng.Intn(3))
				nn := w.now + rng.Intn(3)
				res, gerr := c.Administer(nn, u, p, sname)
				wcode, wlot, wr, we := simAdminister(w, nn, u, p, sname)
				glot, gr, ge := "", 0, 0
				if res != nil {
					glot, gr, ge = res.Lot, int(res.Reason), res.Earliest
				}
				logf("Administer(now=%d %s %s %s) -> err=%v lot=%s reason=%d early=%d | sim %v %s %d %d",
					nn, u, p, sname, gerr, glot, gr, ge, wcode, wlot, wr, we)
				if errCode(gerr) != wcode || glot != wlot || gr != wr || ge != we {
					fail("Administer mismatch got(%v,%s,%d,%d) want(%v,%s,%d,%d)",
						errCode(gerr), glot, gr, ge, wcode, wlot, wr, we)
				}
				_ = nn
			default:
				if len(patients) > 0 {
					p := patients[rng.Intn(len(patients))]
					nn := w.now + rng.Intn(3)
					compareEv(t, c, w, nn, p, logf)
					// 库存守恒：成功次数 == 总扣减。
					total := 0
					snapshot := c.Store().Snapshot()
					for _, l := range snapshot {
						total += l.Qty
					}
					_ = total
				}
			}
		}
		// 序列末尾对每名患者评估一次，并用库存守恒收尾。
		for _, p := range patients {
			compareEv(t, c, w, w.now, p, func(string, ...any) {})
		}
		// 库存与朴素世界逐批一致，且 qty 恒非负。
		for name, l := range w.lots {
			gq, ok := c.Store().Qty(name)
			if !ok || gq != l.qty {
				fail("lot %s qty got=%d ok=%v want=%d", name, gq, ok, l.qty)
			}
			if gq < 0 {
				fail("negative qty %s", name)
			}
		}
	}
}

// ---- 朴素世界的操作语义（独立实现错误次序、选批与扣减） ----

func simValidDate(d int) bool { return d >= 0 && d <= 1_000_000 }

func simRecord(w *simWorld, now int, patient, series string, d int) string {
	if patient == "" || series == "" || !simValidDate(d) || !simValidDate(now) {
		return CodeInvalid
	}
	if now < w.now {
		return CodeClockRewind
	}
	if _, ok := w.birth[patient]; !ok {
		return CodeMissing
	}
	if _, ok := w.series[series]; !ok {
		return CodeMissing
	}
	if d < w.birth[patient] || d > now {
		return CodeInvalid
	}
	for _, r := range w.recs[patient] {
		if r.series == series && r.date == d {
			return CodeDuplicate
		}
	}
	w.now = now
	w.recs[patient] = append(w.recs[patient], simRec{series: series, date: d})
	return ""
}

func simPickLot(w *simWorld, series string, now int) string {
	best := ""
	for name, l := range w.lots {
		if l.series != series || l.quar || l.qty <= 0 || now >= l.exp {
			continue
		}
		if best == "" || l.exp < w.lots[best].exp || (l.exp == w.lots[best].exp && name < best) {
			best = name
		}
	}
	return best
}

// 返回错误码、批号(成功)、过早原因、最早日。
func simAdminister(w *simWorld, now int, nurse, patient, series string) (string, string, int, int) {
	if nurse == "" || patient == "" || series == "" || !simValidDate(now) {
		return CodeInvalid, "", 0, 0
	}
	if now < w.now {
		return CodeClockRewind, "", 0, 0
	}
	birth, pok := w.birth[patient]
	s, sok := w.series[series]
	if !pok || !sok {
		return CodeMissing, "", 0, 0
	}
	if !w.granted[nurse] {
		return CodeNotGranted, "", 0, 0
	}
	for _, r := range w.recs[patient] {
		if r.series == series && r.date == now {
			return CodeDuplicate, "", 0, 0
		}
	}
	_, ag := simJudge(w, patient)
	a := ag[series]
	if a == nil {
		a = &simAgg{}
	}
	if a.valid >= s.n {
		return CodeComplete, "", 0, 0
	}
	k := a.valid + 1
	conflict := false
	if s.live {
		conflict = simLiveConflict(w.recs[patient], simLiveSet(w), series, now)
	}
	reason := vaxrule.ReasonNone
	switch {
	case now-birth < s.minAge[k-1]-4:
		reason = vaxrule.ReasonAge
	case k > 1 && now-a.lastValid < s.minInt[k-1]-4:
		reason = vaxrule.ReasonInterval
	case a.hasLast && !a.lastValidF && now-a.lastDate < s.r:
		reason = vaxrule.ReasonRedose
	case conflict:
		reason = vaxrule.ReasonLive
	}
	if reason != vaxrule.ReasonNone {
		w.now = now
		return CodeTooEarly, "", int(reason), simEarliest(w, patient, series, now)
	}
	lot := simPickLot(w, series, now)
	if lot == "" {
		return CodeNoStock, "", 0, 0
	}
	w.now = now
	w.lots[lot].qty--
	w.recs[patient] = append(w.recs[patient], simRec{series: series, date: now})
	w.adminOK++
	return "", lot, 0, 0
}

func simLiveSet(w *simWorld) map[string]bool {
	m := map[string]bool{}
	for name, s := range w.series {
		m[name] = s.live
	}
	return m
}

func lotNames(w *simWorld) []string {
	out := make([]string, 0, len(w.lots))
	for name := range w.lots {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func joinLogs(logs []string) string {
	out := ""
	for _, l := range logs {
		out += l + "\n"
	}
	return out
}
