package uplink

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"ontology/profile"
)

// ---- 朴素模拟：逐毫秒推进、每毫秒全量扫描所有测点，无任何事件结构 ----

type nPoint struct {
	db, minI, maxI, lo, hi int64
	lastV, lastAt          int64
	hasLast                bool
	curTS, curV            int64
	hasCur                 bool
	bad                    int64
	fault                  bool
	deferAt                int64
}

type naive struct {
	wn, u, q  int64
	clock     int64
	pts       map[string]*nPoint
	names     []string
	used      map[int64]int64
	throttled int64
}

func newNaive(wn, u, q int64) *naive {
	return &naive{wn: wn, u: u, q: q, pts: map[string]*nPoint{}, used: map[int64]int64{}}
}

func nAbs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func (p *nPoint) reportDue() bool {
	if p.fault || !p.hasCur {
		return false
	}
	if p.hasLast && p.curTS <= p.lastAt {
		return false
	}
	if p.hasLast && nAbs(p.curV-p.lastV) <= p.db {
		return false
	}
	return true
}

func (p *nPoint) reportAt() int64 {
	at := p.curTS
	if p.hasLast && p.lastAt+p.minI > at {
		at = p.lastAt + p.minI
	}
	if p.deferAt > at {
		at = p.deferAt
	}
	return at
}

func (p *nPoint) hbDue() bool { return !p.fault && p.hasLast }

func (p *nPoint) hbAt() int64 {
	at := p.lastAt + p.maxI
	if p.deferAt > at {
		at = p.deferAt
	}
	return at
}

func (n *naive) fire(name string, isReport bool, a int64, out *[]string) {
	p := n.pts[name]
	w := a / n.wn
	if n.used[w] >= n.u {
		p.deferAt = (w + 1) * n.wn
		n.throttled++
		return
	}
	n.used[w]++
	reason := "Heartbeat"
	if isReport {
		switch {
		case !p.hasLast:
			reason = "First"
		case a == p.curTS:
			reason = "Change"
		default:
			reason = "Trailing"
		}
	}
	*out = append(*out, fmt.Sprintf("Report(%s,%d,%d,%s)", name, a, p.curV, reason))
	p.lastV, p.hasLast, p.lastAt, p.deferAt = p.curV, true, a, 0
}

// earliest 全量扫描所有测点，返回满足 accept 的最早事件
// （时刻相同按测点名字节序，同测点同刻待报先于心跳）。
func (n *naive) earliest(accept func(at int64) bool) (name string, isReport bool, at int64, ok bool) {
	better := func(nm string, isRep bool, a int64) {
		if !accept(a) {
			return
		}
		if !ok || a < at || (a == at && (nm < name || (nm == name && isRep && !isReport))) {
			name, isReport, at, ok = nm, isRep, a, true
		}
	}
	for _, nm := range n.names {
		p := n.pts[nm]
		if p.reportDue() {
			better(nm, true, p.reportAt())
		}
		if p.hbDue() {
			better(nm, false, p.hbAt())
		}
	}
	return name, isReport, at, ok
}

func (n *naive) tick(t int64, out *[]string) {
	// 热更新可能产生早于当前时钟的逾期事件，先按全局顺序处理。
	for {
		name, isRep, at, ok := n.earliest(func(a int64) bool { return a < n.clock })
		if !ok {
			break
		}
		n.fire(name, isRep, at, out)
	}
	for ms := n.clock; ms <= t; ms++ {
		for {
			name, isRep, at, ok := n.earliest(func(a int64) bool { return a == ms })
			if !ok {
				break
			}
			n.fire(name, isRep, at, out)
		}
	}
}

func (n *naive) checkClock(t int64) error {
	if t < 0 || t > profile.MaxT {
		return profile.ErrInvalid
	}
	if t < n.clock {
		return profile.ErrClockBack
	}
	if t-n.clock > profile.MaxStep {
		return profile.ErrInvalid
	}
	return nil
}

func (n *naive) Tick(t int64) ([]string, error) {
	if err := n.checkClock(t); err != nil {
		return nil, err
	}
	var out []string
	n.tick(t, &out)
	n.clock = t
	return out, nil
}

func (n *naive) Sample(name string, ts, v int64) ([]string, error) {
	if nAbs(v) > profile.MaxAbsV {
		return nil, profile.ErrInvalid
	}
	if err := n.checkClock(ts); err != nil {
		return nil, err
	}
	p, ok := n.pts[name]
	if !ok {
		return nil, profile.ErrNoPoint
	}
	var out []string
	n.tick(ts, &out)
	if v >= p.lo && v <= p.hi {
		p.bad = 0
		p.curTS, p.curV, p.hasCur = ts, v, true
		if p.fault {
			p.fault = false
			out = append(out, fmt.Sprintf("Recover(%s,%d)", name, ts))
			out = append(out, fmt.Sprintf("Report(%s,%d,%d,Recover)", name, ts, v))
			p.lastV, p.hasLast, p.lastAt, p.deferAt = v, true, ts, 0
		}
	} else {
		p.bad++
		if p.bad == n.q && !p.fault {
			p.fault = true
			out = append(out, fmt.Sprintf("Fault(%s,%d)", name, ts))
		}
	}
	n.tick(ts, &out)
	n.clock = ts
	return out, nil
}

func (n *naive) SetProfile(name string, ts, db, minI, maxI, lo, hi int64) ([]string, error) {
	prm := profile.Params{DB: db, MinI: minI, MaxI: maxI, Lo: lo, Hi: hi}
	if !prm.Valid() {
		return nil, profile.ErrInvalid
	}
	if err := n.checkClock(ts); err != nil {
		return nil, err
	}
	var out []string
	n.tick(ts, &out)
	p, ok := n.pts[name]
	if !ok {
		p = &nPoint{}
		n.pts[name] = p
		n.names = append(n.names, name)
		sort.Strings(n.names)
	}
	p.db, p.minI, p.maxI, p.lo, p.hi = db, minI, maxI, lo, hi
	n.tick(ts, &out)
	n.clock = ts
	return out, nil
}

// ---- 随机差分测试：1500 组随机操作序列，与逐毫秒朴素模拟对照 ----

func runRandomSeq(t *testing.T, seq int) (opsLog string, outCount int, throttled, faults int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(seq)*7919 + 1))
	wn := 50 + rng.Int63n(251) // [50,300]
	u := 1 + rng.Int63n(3)     // [1,3]
	q := 1 + rng.Int63n(3)     // [1,3]
	eng, err := New(wn, u, q)
	if err != nil {
		t.Fatal(err)
	}
	nv := newNaive(wn, u, q)
	names := []string{"a", "b", "c", "d"}[:2+rng.Intn(3)]
	var clock int64
	var logBuf strings.Builder
	fmt.Fprintf(&logBuf, "seq=%d 输入: wn=%d u=%d q=%d points=%v\n", seq, wn, u, q, names)

	// 不变量检查用状态。
	type repInfo struct {
		at, minI, ver int64
	}
	lastRep := map[string]repInfo{}
	ver := map[string]int64{}
	winUse := map[int64]int64{}

	fail := func(format string, args ...any) {
		t.Helper()
		args = append([]any{logBuf.String()}, args...)
		t.Fatalf("输入与已产生输出:\n%s\n判定依据: "+format, args...)
	}
	checkInvariants := func(outs []Output) {
		for _, o := range outs {
			if o.Kind != OutReport || o.Reason == profile.ReasonRecover {
				continue
			}
			// 不变量 1：每窗口四类上报合计不超过 U。
			w := o.T / wn
			winUse[w]++
			if winUse[w] > u {
				fail("窗口 %d 上报数 %d 超过 U=%d", w, winUse[w], u)
			}
			// 不变量 2：参数未变时同测点相邻上报时刻差不小于 minI。
			st := eng.points[o.Point]
			if lr, ok := lastRep[o.Point]; ok && lr.ver == ver[o.Point] && o.T-lr.at < lr.minI {
				fail("测点 %s 相邻上报间隔 %d < minI=%d", o.Point, o.T-lr.at, lr.minI)
			}
			lastRep[o.Point] = repInfo{at: o.T, minI: st.MinI, ver: ver[o.Point]}
		}
		// 不变量 3：已有上报的非故障测点 lastAt+maxI 大于当前时钟，除非正被推迟。
		for name, st := range eng.points {
			if st.HasLast && !st.Fault && st.LastAt+st.MaxI <= clock && st.Defer <= clock {
				fail("测点 %s lastAt+maxI=%d <= clock=%d 且 defer=%d", name, st.LastAt+st.MaxI, clock, st.Defer)
			}
		}
	}

	outCount = 0
	for i := 0; i < 30; i++ {
		ts := clock + rng.Int63n(400)
		var opDesc string
		var eo []Output
		var ee error
		var no []string
		var ne error
		switch kind := rng.Intn(12); {
		case kind <= 1: // Tick
			opDesc = fmt.Sprintf("Tick(%d)", ts)
			eo, ee = eng.Tick(ts)
			no, ne = nv.Tick(ts)
		case kind <= 4: // SetProfile（建立或热更新）
			name := names[rng.Intn(len(names))]
			if rng.Intn(15) == 0 {
				name = "zz"
			}
			prm := [5]int64{rng.Int63n(11), 1 + rng.Int63n(200), 1000 + rng.Int63n(500), -rng.Int63n(51), 50 + rng.Int63n(51)}
			if rng.Intn(10) == 0 {
				prm[1] = prm[2] // minI==maxI，非法参数
			}
			opDesc = fmt.Sprintf("SetProfile(%s,%d,%v)", name, ts, prm)
			eo, ee = eng.SetProfile(name, ts, prm[0], prm[1], prm[2], prm[3], prm[4])
			no, ne = nv.SetProfile(name, ts, prm[0], prm[1], prm[2], prm[3], prm[4])
			if ee == nil {
				ver[name]++
			}
		default: // Sample
			name := names[rng.Intn(len(names))]
			if rng.Intn(20) == 0 {
				name = "zz" // 不存在的测点
			}
			v := rng.Int63n(201) - 100
			switch rng.Intn(15) {
			case 0:
				v = profile.MaxAbsV + 1 // 非法取值
			case 1:
				v = -v
			}
			if rng.Intn(25) == 0 && clock > 10 {
				ts = clock - 1 - rng.Int63n(10) // 时钟回退
			}
			opDesc = fmt.Sprintf("Sample(%s,%d,%d)", name, ts, v)
			eo, ee = eng.Sample(name, ts, v)
			no, ne = nv.Sample(name, ts, v)
		}
		if (ee == nil) != (ne == nil) || (ee != nil && ee != ne) {
			fail("操作 %s: 引擎 err=%v, 朴素模拟 err=%v", opDesc, ee, ne)
		}
		got := fmtOuts(eo)
		if !reflect.DeepEqual(got, no) {
			fail("操作 %s:\n引擎输出=%v\n朴素输出=%v", opDesc, got, no)
		}
		fmt.Fprintf(&logBuf, "%s -> err=%v 输出=%v\n", opDesc, ee, got)
		outCount += len(got)
		for _, o := range eo {
			if o.Kind == OutFault {
				faults++
			}
		}
		if ee == nil {
			clock = ts
			checkInvariants(eo)
		}
	}
	if eng.Throttled != nv.throttled {
		fail("Throttled: 引擎=%d, 朴素模拟=%d", eng.Throttled, nv.throttled)
	}
	// 终态逐字段对照。
	if len(eng.points) != len(nv.pts) {
		fail("测点数: 引擎=%d, 朴素模拟=%d", len(eng.points), len(nv.pts))
	}
	for name, st := range eng.points {
		np := nv.pts[name]
		if np == nil {
			fail("朴素模拟缺少测点 %s", name)
		}
		if st.DB != np.db || st.MinI != np.minI || st.MaxI != np.maxI || st.Lo != np.lo || st.Hi != np.hi ||
			st.LastV != np.lastV || st.HasLast != np.hasLast || st.LastAt != np.lastAt ||
			st.CurTS != np.curTS || st.CurV != np.curV || st.HasCur != np.hasCur ||
			st.Bad != np.bad || st.Fault != np.fault || st.Defer != np.deferAt {
			fail("测点 %s 终态不一致: 引擎=%+v, 朴素模拟=%+v", name, st, np)
		}
	}
	return logBuf.String(), outCount, eng.Throttled, faults
}

func TestRandomVsNaive(t *testing.T) {
	const seqs = 1500
	var throttledSeqs, faultSeqs, totalOuts int
	for seq := 0; seq < seqs; seq++ {
		seq := seq
		t.Run(fmt.Sprintf("seq%d", seq), func(t *testing.T) {
			opsLog, outCount, throttled, faults := runRandomSeq(t, seq)
			if throttled > 0 {
				throttledSeqs++
			}
			if faults > 0 {
				faultSeqs++
			}
			totalOuts += outCount
			t.Logf("判定依据: 引擎输出序列与逐毫秒朴素模拟逐步一致\n%s输出总数=%d", opsLog, outCount)
		})
	}
	t.Logf("覆盖统计: %d 组序列, 含推迟的序列=%d, 含故障的序列=%d, 输出总数=%d",
		seqs, throttledSeqs, faultSeqs, totalOuts)
}

// 相同操作序列重放结果相同。
func TestReplayDeterminism(t *testing.T) {
	run := func() []string {
		rng := rand.New(rand.NewSource(42))
		e, err := New(100, 2, 2)
		if err != nil {
			t.Fatal(err)
		}
		var all []string
		var clock int64
		for i := 0; i < 60; i++ {
			clock += rng.Int63n(300)
			var outs []Output
			var err error
			switch rng.Intn(3) {
			case 0:
				outs, err = e.Tick(clock)
			case 1:
				outs, err = e.SetProfile("a", clock, rng.Int63n(6), 1+rng.Int63n(100), 1000+rng.Int63n(500), -50, 50)
			default:
				outs, err = e.Sample("a", clock, rng.Int63n(201)-100)
			}
			if err != nil {
				all = append(all, "ERR:"+err.Error())
				continue
			}
			all = append(all, fmtOuts(outs)...)
		}
		return all
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放结果不同:\n%v\n%v", first, second)
	}
}
