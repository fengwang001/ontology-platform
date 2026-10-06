package delay

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

const airportsCount = 4

type rngWorld struct {
	fs  []Flight
	cfg Config
}

// genWorld 生成合法随机航班表：每架飞机一段机场连续行程；
// 机组用贪心（当前所在机场匹配则复用）指派，保证机组链同样连续。
// genWorld 生成合法随机航班表：先生成若干条机场连续的机组行程，
// 每条机组行程结构上复用为一条飞机链；再随机交换同 OD 的两段的
// 飞机归属，制造飞机/机组交叉而不破坏任何链的机场连续性。
// genWorld 生成合法随机航班表。先生成若干条机场连续的机组行程；
// 再以“时间不重叠且首尾机场衔接”的贪心方式把机组段装进有限架飞机：
// 每架飞机按时间顺序承接段，要求该段起飞机场等于该机上一段到达机场。
// 装不下的段使用新飞机。该构造保证飞机链与机组链都机场连续，
// 同时一架飞机可服务多个机组（链交叉）。
func genWorld(rng *rand.Rand) rngWorld {
	ports := []string{"P0", "P1", "P2", "P3"}
	nCrew := 1 + rng.Intn(3)
	type seed struct {
		crew             int
		origin, dest     string
		sched, dur, orig int
	}
	var seeds []seed
	for cw := 0; cw < nCrew; cw++ {
		cur := ports[rng.Intn(airportsCount)]
		tm := 50 + rng.Intn(50)
		for k, n := 0, 1+rng.Intn(5); k < n; k++ {
			dst := ports[rng.Intn(airportsCount)]
			for dst == cur {
				dst = ports[rng.Intn(airportsCount)]
			}
			dur := 5 + rng.Intn(40)
			seeds = append(seeds, seed{
				crew: cw, origin: cur, dest: dst, sched: tm, dur: dur, orig: len(seeds),
			})
			cur = dst
			tm += dur + 10 + rng.Intn(80)
		}
	}

	order := make([]int, len(seeds))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return seeds[order[i]].sched < seeds[order[j]].sched })

	type acState struct {
		lastArr  int
		lastDest string
		used     bool
	}
	var acs []*acState
	acOf := make([]int, len(seeds))
	for _, oi := range order {
		s := seeds[oi]
		cand := []int{}
		for ai, st := range acs {
			if !st.used || (st.lastDest == s.origin && st.lastArr <= s.sched) {
				cand = append(cand, ai)
			}
		}
		var ai int
		if len(cand) > 0 {
			ai = cand[rng.Intn(len(cand))]
		} else {
			ai = len(acs)
			acs = append(acs, &acState{})
		}
		acs[ai].used = true
		acs[ai].lastArr = s.sched + s.dur
		acs[ai].lastDest = s.dest
		acOf[oi] = ai
	}

	sort.SliceStable(order, func(i, j int) bool { return seeds[order[i]].sched < seeds[order[j]].sched })
	fs := make([]Flight, len(seeds))
	for rank, oi := range order {
		s := seeds[oi]
		fs[rank] = Flight{
			ID:         fmt.Sprintf("F%03d", rank),
			Scheduled:  s.sched,
			Duration:   s.dur,
			Origin:     s.origin,
			Dest:       s.dest,
			AircraftID: fmt.Sprintf("A%d", acOf[oi]),
			CrewID:     fmt.Sprintf("C%d", s.crew),
		}
	}

	cfg := Config{
		MinTurnaround: []int{0, 5, 15}[rng.Intn(3)],
		MinConnection: []int{0, 5, 15}[rng.Intn(3)],
		DutyLimit:     80 + rng.Intn(300),
		Curfews:       map[string]Interval{},
	}
	if rng.Intn(2) == 0 {
		p := ports[rng.Intn(airportsCount)]
		start := 30 + rng.Intn(400)
		cfg.Curfews[p] = Interval{Start: start, End: start + 20 + rng.Intn(60)}
	}
	return rngWorld{fs, cfg}
}

func dumpInj(inj map[string]map[string]Injection) string {
	ids := make([]string, 0, len(inj))
	for k := range inj {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, k := range ids {
		refs := make([]string, 0)
		for r := range inj[k] {
			refs = append(refs, r)
		}
		sort.Strings(refs)
		for _, r := range refs {
			fmt.Fprintf(&b, " %s/%s=%+v", k, r, inj[k][r])
		}
	}
	if b.Len() == 0 {
		return "(none)"
	}
	return b.String()
}

func putInj(inj map[string]map[string]Injection, fid, ref string, v Injection) {
	m := inj[fid]
	if m == nil {
		m = map[string]Injection{}
		inj[fid] = m
	}
	m[ref] = v
}

// TestRandomDifferential：大量随机航班表 + 随机注入/撤回序列，
// 每步将引擎结论与独立朴素模型逐航班比对，并打印输入与判定依据。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	for trial := 0; trial < 400; trial++ {
		w := genWorld(rng)
		e, err := New(w.fs, w.cfg)
		if err != nil {
			t.Fatalf("trial %d New: %v", trial, err)
		}
		inj := map[string]map[string]Injection{}
		type ref struct{ fid, r string }
		var active []ref

		for op := 0; op < 2+rng.Intn(10); op++ {
			f := w.fs[rng.Intn(len(w.fs))]
			desc := ""
			if len(active) > 0 && rng.Intn(3) == 0 {
				pick := active[rng.Intn(len(active))]
				desc = fmt.Sprintf("withdraw %s/%s", pick.fid, pick.r)
				if err := e.Withdraw(0, pick.fid, pick.r); err == nil {
					delete(inj[pick.fid], pick.r)
					if len(inj[pick.fid]) == 0 {
						delete(inj, pick.fid)
					}
				}
			} else {
				rr := fmt.Sprintf("r%d", rng.Intn(4))
				if rng.Intn(4) == 0 {
					desc = fmt.Sprintf("cancel %s/%s", f.ID, rr)
					if err := e.InjectCancel(0, f.ID, rr); err == nil {
						putInj(inj, f.ID, rr, Injection{Cancel: true})
						active = append(active, ref{f.ID, rr})
					}
				} else {
					d := rng.Intn(120)
					desc = fmt.Sprintf("delay %s/%s d=%d", f.ID, rr, d)
					if err := e.InjectDelay(0, f.ID, rr, d); err == nil {
						putInj(inj, f.ID, rr, Injection{Delay: d})
						active = append(active, ref{f.ID, rr})
					}
				}
			}

			want, nerr := NaiveSweep(w.fs, w.cfg, inj, nil)
			if nerr != nil {
				t.Fatalf("trial %d naive: %v", trial, nerr)
			}
			mismatch := ""
			for _, fl := range w.fs {
				g, _ := e.Get(fl.ID)
				if g != want[fl.ID] {
					mismatch = fmt.Sprintf("flight %s got %+v want %+v", fl.ID, g, want[fl.ID])
					break
				}
			}
			t.Logf("trial %3d op %2d | %-34s | inj:%s", trial, op, desc, dumpInj(inj))
			if mismatch != "" {
				t.Fatalf("trial %d op %d %s\n%s\ncfg=%+v", trial, op, desc, mismatch, w.cfg)
			}
		}
	}
}

// TestRandomFrozenDifferential 在时钟推进、部分航班冻结的场景下对照。
func TestRandomFrozenDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	for trial := 0; trial < 300; trial++ {
		w := genWorld(rng)
		e, err := New(w.fs, w.cfg)
		if err != nil {
			t.Fatal(err)
		}
		inj := map[string]map[string]Injection{}
		clock := 30
		for op := 0; op < 1+rng.Intn(6); op++ {
			clock += rng.Intn(40)
			// 当前已起飞航班快照，供朴素模型固定。
			frozen := snapshotFrozen(e, w, clock)

			// 只对此刻“未取消且未起飞”的航班做注入，避免与冻结耦合的歧义。
			var pool []Flight
			for _, fl := range w.fs {
				g, _ := e.Get(fl.ID)
				if g.Status != StatusCanceled && g.ActualDep > clock {
					pool = append(pool, fl)
				}
			}
			if len(pool) == 0 {
				continue
			}
			f := pool[rng.Intn(len(pool))]
			desc := ""
			if rng.Intn(2) == 0 {
				d := rng.Intn(100)
				desc = fmt.Sprintf("delay %s d=%d", f.ID, d)
				if err := e.InjectDelay(clock, f.ID, "r0", d); err == nil {
					putInj(inj, f.ID, "r0", Injection{Delay: d})
				} else {
					desc += "(" + err.Error() + ")"
				}
			} else {
				desc = fmt.Sprintf("cancel %s", f.ID)
				if err := e.InjectCancel(clock, f.ID, "r0"); err == nil {
					putInj(inj, f.ID, "r0", Injection{Cancel: true})
				} else {
					desc += "(" + err.Error() + ")"
				}
			}

			want, _ := NaiveSweep(w.fs, w.cfg, inj, frozen)
			for _, fl := range w.fs {
				if _, isFrozen := frozen[fl.ID]; isFrozen {
					continue
				}
				g, _ := e.Get(fl.ID)
				if g != want[fl.ID] {
					var dbg strings.Builder
					for _, d := range w.fs {
						gg, _ := e.Get(d.ID)
						_, frz := frozen[d.ID]
						fmt.Fprintf(&dbg, "\n  %s sch=%d %s->%s ac=%s cr=%s | eng=%+v | nai=%+v",
							d.ID, d.Scheduled, d.Origin, d.Dest, d.AircraftID, d.CrewID, gg, want[d.ID])
						if frz {
							fmt.Fprintf(&dbg, " FROZEN=%+v", frozen[d.ID])
						}
					}
					t.Fatalf("trial %d %s now=%d: got %+v want %+v inj=%s frozen=%d cfg=%+v%s",
						trial, desc, clock, g, want[fl.ID], dumpInj(inj), len(frozen), w.cfg, dbg.String())
				}
			}
			t.Logf("frozen trial %3d now=%3d | %-24s | frozen=%d inj:%s",
				trial, clock, desc, len(frozen), dumpInj(inj))
		}
	}
}

// snapshotFrozen 返回“当前时刻之前已发生”的航班固定结论：
// 任何实际起飞时刻 <= now 的执行段、以及在此时刻之前已确定的取消段。
// 朴素模型用这些固定结论从头重放，只对 now 之后的航班重新推算。
func snapshotFrozen(e *Engine, w rngWorld, now int) map[string]Result {
	frozen := map[string]Result{}
	for _, fl := range w.fs {
		g, _ := e.Get(fl.ID)
		if g.ActualDep <= now {
			if g.Status == StatusCanceled {
				continue // 取消段不冻结，由注入重新推出
			}
			frozen[fl.ID] = g
		}
	}
	return frozen
}

// TestIncrementalCost 用 LastSwept 计数证明重算只触及受影响航班：
// 表扩大 10 倍，单链末端一个延误的处理航班数恒为 1。
func TestIncrementalCost(t *testing.T) {
	build := func(n int) *Engine {
		fs := make([]Flight, n)
		for a := 0; a < n; a++ {
			fs[a] = Flight{
				ID: fmt.Sprintf("F%d", a), Scheduled: 100 + a*100, Duration: 10,
				Origin: "X", Dest: "X", AircraftID: fmt.Sprintf("A%d", a),
				CrewID: fmt.Sprintf("C%d", a),
			}
		}
		e, err := New(fs, Config{DutyLimit: 1 << 30})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	e100 := build(100)
	e1000 := build(1000)
	if err := e100.InjectDelay(0, "F50", "d", 5); err != nil {
		t.Fatal(err)
	}
	if err := e1000.InjectDelay(0, "F500", "d", 5); err != nil {
		t.Fatal(err)
	}
	if e100.LastSwept() != 1 || e1000.LastSwept() != 1 {
		t.Fatalf("incremental cost grew with table size: %d vs %d",
			e100.LastSwept(), e1000.LastSwept())
	}
	if err := e1000.Withdraw(10, "F500", "d"); err != nil {
		t.Fatal(err)
	}
	if e1000.LastSwept() != 1 {
		t.Fatalf("withdraw cost: %d", e1000.LastSwept())
	}
}

// TestGlobalInvariants 每步校验过站、衔接、宵禁与值勤上限全部满足。
func TestGlobalInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 150; trial++ {
		w := genWorld(rng)
		e, err := New(w.fs, w.cfg)
		if err != nil {
			t.Fatal(err)
		}
		for op := 0; op < 8; op++ {
			f := w.fs[rng.Intn(len(w.fs))]
			if rng.Intn(2) == 0 {
				_ = e.InjectDelay(0, f.ID, "d", rng.Intn(150))
			} else {
				_ = e.InjectCancel(0, f.ID, "c")
			}
			checkInvariants(t, e, w)
		}
	}
}

func checkInvariants(t *testing.T, e *Engine, w rngWorld) {
	t.Helper()
	gapA, gapC := w.cfg.MinTurnaround, w.cfg.MinConnection
	check := func(chains map[string][]*flight, gap int, label string) {
		for _, list := range chains {
			var lastArr int
			hasLast := false
			for _, f := range list {
				c := e.conc[f.ID]
				if c.status == StatusCanceled {
					continue
				}
				if hasLast && c.dep < lastArr+gap {
					t.Fatalf("%s gap violated: %s dep=%d prevArr=%d gap=%d",
						label, f.ID, c.dep, lastArr, gap)
				}
				if cf := w.cfg.Curfews[f.Origin]; cf.Contains(c.dep) {
					t.Fatalf("%s dep in curfew: %s dep=%d", label, f.ID, c.dep)
				}
				if cf := w.cfg.Curfews[f.Dest]; cf.Contains(c.arr) {
					t.Fatalf("%s arr in curfew: %s arr=%d", label, f.ID, c.arr)
				}
				lastArr, hasLast = c.arr, true
			}
		}
	}
	check(e.chainsA, gapA, "aircraft")
	check(e.chainsC, gapC, "crew")
	for _, list := range e.chainsC {
		var firstDep int
		hasFirst := false
		for _, f := range list {
			c := e.conc[f.ID]
			if c.status == StatusCanceled {
				continue
			}
			if !hasFirst {
				firstDep, hasFirst = c.dep, true
			}
			if c.arr-firstDep > w.cfg.DutyLimit {
				t.Fatalf("duty exceeded on crew chain: %s span=%d", f.ID, c.arr-firstDep)
			}
		}
	}
}

// TestReplayDeterminism 同一航班表与注入集合两次重放，逐航班结论一致。
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(31337))
	for trial := 0; trial < 100; trial++ {
		w := genWorld(rng)
		script := []struct {
			fid, ref string
			cancel   bool
			delay    int
		}{}
		for i := 0; i < 8; i++ {
			f := w.fs[rng.Intn(len(w.fs))]
			cancel := rng.Intn(3) == 0
			script = append(script, struct {
				fid, ref string
				cancel   bool
				delay    int
			}{f.ID, "r", cancel, rng.Intn(120)})
		}
		run := func() map[string]Result {
			e, err := New(w.fs, w.cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range script {
				if s.cancel {
					_ = e.InjectCancel(0, s.fid, s.ref)
				} else {
					_ = e.InjectDelay(0, s.fid, s.ref, s.delay)
				}
			}
			out := map[string]Result{}
			for _, f := range w.fs {
				out[f.ID], _ = e.Get(f.ID)
			}
			return out
		}
		a, b := run(), run()
		for id, r := range a {
			if r != b[id] {
				t.Fatalf("trial %d non-deterministic replay for %s: %+v vs %+v",
					trial, id, r, b[id])
			}
		}
	}
}
