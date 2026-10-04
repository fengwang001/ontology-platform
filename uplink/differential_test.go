package uplink_test

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/profile"
	"ontology/uplink"
)

// refState 是与被测实现完全独立书写的测点状态，严格按题目规则判定事件。
type refState struct {
	prm                    profile.Params
	lastV, lastAt          int64
	curV, curAt            int64
	bad, deferUntil        int64
	hasLast, hasCur, fault bool
}

func abs64(a, b int64) int64 {
	d := a - b
	if d >= 0 {
		return d
	}
	return -d
}

func max3i(a, b, c int64) int64 {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	return a
}

func (s *refState) pendingAt() (int64, bool) {
	if s.fault || !s.hasCur {
		return 0, false
	}
	if s.hasLast {
		if s.curAt <= s.lastAt || abs64(s.curV, s.lastV) <= s.prm.DB {
			return 0, false
		}
		return max3i(s.lastAt+s.prm.MinInterval, s.curAt, s.deferUntil), true
	}
	if s.curAt < s.deferUntil {
		return s.deferUntil, true
	}
	return s.curAt, true
}

func (s *refState) heartbeatAt() (int64, bool) {
	if s.fault || !s.hasLast {
		return 0, false
	}
	at := s.lastAt + s.prm.MaxInterval
	if s.deferUntil > at {
		at = s.deferUntil
	}
	return at, true
}

func (s *refState) ingest(t, v, q int64) (recover, faulted bool) {
	if s.prm.Low <= v && v <= s.prm.High {
		s.bad = 0
		s.curAt, s.curV, s.hasCur = t, v, true
		if s.fault {
			s.fault = false
			s.lastV, s.lastAt, s.hasLast = v, t, true
			s.deferUntil = 0
			return true, false
		}
		return false, false
	}
	s.bad++
	if s.bad == q && !s.fault {
		s.fault = true
		return false, true
	}
	return false, false
}

type refEngine struct {
	points          map[string]*refState
	used            map[int64]int64
	wn, u, q, clock int64
	throttled       int64
}

func newRef(wn, u, q int64) *refEngine {
	return &refEngine{
		points: map[string]*refState{},
		used:   map[int64]int64{},
		wn:     wn, u: u, q: q,
	}
}

// drain 反复取全部测点中时刻不大于 t 的最早事件（名字节序、待报优先）。
func (n *refEngine) drain(t int64) []uplink.Event {
	var out []uplink.Event
	for {
		names := make([]string, 0, len(n.points))
		for name := range n.points {
			names = append(names, name)
		}
		sort.Strings(names)
		bestName := ""
		bestAt := int64(0)
		bestHB, found := false, false
		for _, name := range names {
			pt := n.points[name]
			if at, ok := pt.pendingAt(); ok && at <= t {
				bestName, bestAt, bestHB, found = name, at, false, true
				break
			}
			if at, ok := pt.heartbeatAt(); ok && at <= t {
				bestName, bestAt, bestHB, found = name, at, true, true
				break
			}
		}
		if !found {
			break
		}
		pt := n.points[bestName]
		win := bestAt / n.wn
		if n.used[win] >= n.u {
			pt.deferUntil = (win + 1) * n.wn
			n.throttled++
			continue
		}
		v := pt.curV
		var why uplink.Reason
		switch {
		case bestHB:
			why = uplink.Heartbeat
		case !pt.hasLast:
			why = uplink.First
		case bestAt == pt.curAt:
			why = uplink.Change
		default:
			why = uplink.Trailing
		}
		pt.lastV, pt.lastAt, pt.hasLast = v, bestAt, true
		pt.deferUntil = 0
		n.used[win]++
		out = append(out, uplink.Event{
			Kind: uplink.KindReport, Point: bestName, At: bestAt, V: v, Reason: why,
		})
	}
	return out
}

// advance 朴素地逐毫秒跑满 clock+1..t（模拟时钟自然推进）。
func (n *refEngine) advance(t int64) []uplink.Event {
	var out []uplink.Event
	for ms := n.clock + 1; ms <= t; ms++ {
		out = append(out, n.drain(ms)...)
	}
	n.clock = t
	return out
}

func (n *refEngine) setProfile(name string, t int64, p profile.Params) []uplink.Event {
	out := n.advance(t)
	pt, ok := n.points[name]
	if !ok {
		pt = &refState{}
		n.points[name] = pt
	}
	pt.prm = p
	out = append(out, n.drain(t)...)
	return out
}

func (n *refEngine) sample(name string, t, v int64) []uplink.Event {
	out := n.advance(t)
	pt := n.points[name]
	recovered, faulted := pt.ingest(t, v, n.q)
	if faulted {
		out = append(out, uplink.Event{Kind: uplink.KindFault, Point: name, At: t})
	} else if recovered {
		out = append(out,
			uplink.Event{Kind: uplink.KindRecover, Point: name, At: t},
			uplink.Event{Kind: uplink.KindReport, Point: name, At: t, V: v, Reason: uplink.Recover},
		)
	}
	out = append(out, n.drain(t)...)
	return out
}

// ----- 随机操作序列生成与对拍 -----

type rndOp struct {
	kind                         string
	point                        string
	t, db, minI, maxI, lo, hi, v int64
}

func genSequence(r *rand.Rand, maxT int64) ([]rndOp, int64, int64, int64) {
	wn := int64(1<<r.Intn(4) + 8)
	u := int64(1 + r.Intn(3))
	q := int64(1 + r.Intn(3))

	const nPoints = 4
	params := make([]profile.Params, nPoints)
	for i := range params {
		lo := int64(-50 + r.Intn(40))
		hi := lo + int64(r.Intn(80))
		params[i] = profile.Params{
			DB:          int64(r.Intn(12)),
			MinInterval: int64(1 + r.Intn(30)),
			MaxInterval: int64(1000 + r.Intn(400)),
			Low:         lo,
			High:        hi,
		}
	}

	ops := []rndOp{}
	for i := 0; i < nPoints; i++ {
		p := params[i]
		ops = append(ops, rndOp{
			kind: "P", point: string(rune('a' + i)), t: 0,
			db: p.DB, minI: p.MinInterval, maxI: p.MaxInterval, lo: p.Low, hi: p.High,
		})
	}

	t := int64(0)
	nOps := 60 + r.Intn(120)
	for step := 0; step < nOps; step++ {
		t += int64(r.Intn(40))
		if t > maxT {
			t = maxT
		}
		pi := r.Intn(nPoints)
		name := string(rune('a' + pi))
		p := params[pi]
		switch r.Intn(10) {
		case 0:
			np := profile.Params{
				DB:          int64(r.Intn(12)),
				MinInterval: int64(1 + r.Intn(30)),
				MaxInterval: int64(1000 + r.Intn(400)),
				Low:         int64(-50 + r.Intn(40)),
			}
			np.High = np.Low + int64(r.Intn(80))
			params[pi] = np
			ops = append(ops, rndOp{
				kind: "P", point: name, t: t,
				db: np.DB, minI: np.MinInterval, maxI: np.MaxInterval, lo: np.Low, hi: np.High,
			})
		case 1:
			ops = append(ops, rndOp{kind: "T", t: t})
		default:
			v := p.Low + int64(r.Intn(int(p.High-p.Low)+30)) - 15
			ops = append(ops, rndOp{kind: "S", point: name, t: t, v: v})
		}
	}
	ops = append(ops, rndOp{kind: "T", t: maxT})
	return ops, wn, u, q
}

func runReference(ops []rndOp, wn, u, q int64) []uplink.Event {
	n := newRef(wn, u, q)
	var out []uplink.Event
	for _, o := range ops {
		switch o.kind {
		case "P":
			out = append(out, n.setProfile(o.point, o.t,
				profile.Params{DB: o.db, MinInterval: o.minI, MaxInterval: o.maxI, Low: o.lo, High: o.hi})...)
		case "S":
			out = append(out, n.sample(o.point, o.t, o.v)...)
		case "T":
			out = append(out, n.advance(o.t)...)
		}
	}
	return out
}

func runEngine(ops []rndOp, wn, u, q int64) ([]uplink.Event, int64, int64) {
	e, err := uplink.NewEngine(wn, u, q)
	if err != nil {
		panic(err)
	}
	var out []uplink.Event
	for _, o := range ops {
		switch o.kind {
		case "P":
			ev, err := e.SetProfile(o.point, o.t, o.db, o.minI, o.maxI, o.lo, o.hi)
			if err != nil {
				panic(fmt.Sprintf("engine setprofile: %v", err))
			}
			out = append(out, ev...)
		case "S":
			ev, err := e.Sample(o.point, o.t, o.v)
			if err != nil {
				panic(fmt.Sprintf("engine sample: %v", err))
			}
			out = append(out, ev...)
		case "T":
			ev, err := e.Tick(o.t)
			if err != nil {
				panic(fmt.Sprintf("engine tick: %v", err))
			}
			out = append(out, ev...)
		}
	}
	return out, e.Throttled(), e.Popped()
}

func opString(o rndOp) string {
	switch o.kind {
	case "P":
		return fmt.Sprintf("SetProfile(%s,t=%d,db=%d,minI=%d,maxI=%d,[%d,%d])",
			o.point, o.t, o.db, o.minI, o.maxI, o.lo, o.hi)
	case "S":
		return fmt.Sprintf("Sample(%s,t=%d,v=%d)", o.point, o.t, o.v)
	default:
		return fmt.Sprintf("Tick(%d)", o.t)
	}
}

func equalEvents(a, b []uplink.Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func windowQuotaOK(ev []uplink.Event, wn, u int64) bool {
	cnt := map[int64]int64{}
	for _, e := range ev {
		if e.Kind != uplink.KindReport || e.Reason == uplink.Recover {
			continue
		}
		cnt[e.At/wn]++
		if cnt[e.At/wn] > u {
			return false
		}
	}
	return true
}

// TestDifferential1500：1500 组随机序列与独立逐毫秒朴素模型逐事件对拍，含不变量。
// 使用多个种子（每种子 1500 组）扩大随机覆盖。
func TestDifferential1500(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	const maxT int64 = 2500
	seeds := []int64{1, 42, 1234567, 987654321}
	total := 0
	for _, seed := range seeds {
		r := rand.New(rand.NewSource(seed))
		for g := 0; g < 1500; g++ {
			total++
			ops, wn, u, q := genSequence(r, maxT)
			ref := runReference(ops, wn, u, q)
			got, throttled, popped := runEngine(ops, wn, u, q)

			if !equalEvents(ref, got) {
				var b strings.Builder
				fmt.Fprintf(&b, "种子%d 组%d wn=%d u=%d q=%d 不一致\n判定依据 输入:\n", seed, g, wn, u, q)
				for _, o := range ops {
					fmt.Fprintf(&b, "  %s\n", opString(o))
				}
				fmt.Fprintf(&b, "朴素(%d): %s\n引擎(%d): %s\n",
					len(ref), evs(ref), len(got), evs(got))
				t.Fatalf("%s", b.String())
			}
			var reports int64
			for _, ev := range got {
				if ev.Kind == uplink.KindReport && ev.Reason != uplink.Recover {
					reports++
				}
			}
			if popped != reports+throttled {
				t.Fatalf("种子%d 组%d popped=%d reports=%d throttled=%d，违反 popped 不变量",
					seed, g, popped, reports, throttled)
			}
			if !windowQuotaOK(got, wn, u) {
				t.Fatalf("种子%d 组%d 窗口额度超限", seed, g)
			}
		}
	}
	t.Logf("%d 组随机序列与独立逐毫秒朴素模型逐事件一致，popped/窗口额度不变量成立", total)
}

// TestPoppedScaleIndependent：100 与 10000 测点两档同样产生 3 条上报，popped 相同。
func TestPoppedScaleIndependent(t *testing.T) {
	run := func(nPoints int) (reports, throttled, popped int64) {
		// 窗口 0：3 条 First 在 t=0 发出；Tick(100) 的 3 条尾随同在窗口 0，
		// 故 U=6 保证它们都能发出，构造“同样产生 3 条上报”的受控对照。
		e, _ := uplink.NewEngine(1000, 6, 2)
		for i := 0; i < nPoints; i++ {
			name := fmt.Sprintf("p%06d", i)
			if _, err := e.SetProfile(name, 0, 5, 100, 100000, 0, 100); err != nil {
				t.Fatalf("setprofile: %v", err)
			}
		}
		for i := 0; i < 3; i++ {
			name := fmt.Sprintf("p%06d", i)
			if _, err := e.Sample(name, 0, 50); err != nil {
				t.Fatalf("sample %s: err=%v", name, err)
			}
		}
		beforeTick := e.Popped()
		beforeTh := e.Throttled()
		// 第二次样本越死区但受 minI=100 抑制，统一在 Tick(100) 尾随发出 3 条。
		for i := 0; i < 3; i++ {
			name := fmt.Sprintf("p%06d", i)
			if ev, err := e.Sample(name, 50, 75); err != nil || len(ev) != 0 {
				t.Fatalf("sample2 %s: ev=%v err=%v", name, ev, err)
			}
		}
		ev, err := e.Tick(100)
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		var reps int64
		for _, x := range ev {
			if x.Kind == uplink.KindReport {
				reps++
			}
		}
		return reps, e.Throttled() - beforeTh, e.Popped() - beforeTick
	}

	r100, th100, pop100 := run(100)
	r10k, th10k, pop10k := run(10000)
	if r100 != 3 || r10k != 3 {
		t.Fatalf("两档都应恰有 3 条上报, got %d and %d", r100, r10k)
	}
	if pop100 != pop10k || pop100 != r100+th100 {
		t.Fatalf("popped 应与测点数无关且=发出+推迟: 100档 popped=%d(r=%d,th=%d) 10000档 popped=%d(r=%d,th=%d)",
			pop100, r100, th100, pop10k, r10k, th10k)
	}
	t.Logf("规模无关对照：100 测点 popped=%d，10000 测点 popped=%d（均=3 发出+%d 推迟）",
		pop100, pop10k, th100)
}
