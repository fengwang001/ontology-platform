package flights

import (
	"cmp"
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

// genCase 随机生成合法航班表与配置：飞机链按机场衔接生成，机组按时间贪心分配。
func genCase(r *rand.Rand) ([]Flight, Config) {
	airports := []string{"A", "B", "C", "D"}
	cfg := Config{
		MinTurnaround: 5 + r.Intn(30),
		MinConnection: 5 + r.Intn(30),
		Curfews:       map[string]Curfew{},
	}
	if r.Intn(2) == 0 {
		cfg.MaxDuty = 400 + r.Intn(600)
	} else {
		cfg.MaxDuty = 100000
	}
	for _, ap := range airports {
		if r.Intn(2) == 0 {
			start := r.Intn(500)
			cfg.Curfews[ap] = Curfew{Start: start, End: start + r.Intn(200)}
		}
	}
	var flights []Flight
	nextDep := 0 // 保证全局计划起飞时刻互不相同
	for a, nAircraft := 0, 1+r.Intn(3); a < nAircraft; a++ {
		origin := airports[r.Intn(len(airports))]
		dep := r.Intn(120)
		for i, legs := 0, 1+r.Intn(5); i < legs; i++ {
			dep = max(dep, nextDep)
			dur := 30 + r.Intn(180)
			dest := airports[r.Intn(len(airports))]
			flights = append(flights, Flight{
				ID: fmt.Sprintf("F%d", len(flights)), SchedDep: dep, Duration: dur,
				Origin: origin, Dest: dest, Aircraft: fmt.Sprintf("AC%d", a),
			})
			nextDep = dep + 1
			dep += dur + cfg.MinTurnaround + r.Intn(150)
			origin = dest
		}
	}
	slices.SortFunc(flights, func(x, y Flight) int {
		if c := cmp.Compare(x.SchedDep, y.SchedDep); c != 0 {
			return c
		}
		return cmp.Compare(x.ID, y.ID)
	})
	type crewState struct {
		lastDest string
		lastArr  int
		has      bool
	}
	var crews []crewState
	for i := range flights {
		f := &flights[i]
		var cand []int
		for j, cs := range crews {
			if cs.has && cs.lastDest == f.Origin && cs.lastArr+cfg.MinConnection <= f.SchedDep {
				cand = append(cand, j)
			}
		}
		idx := -1
		if len(cand) > 0 && r.Intn(10) < 7 {
			idx = cand[r.Intn(len(cand))]
		} else {
			idx = len(crews)
			crews = append(crews, crewState{})
		}
		f.Crew = fmt.Sprintf("K%d", idx)
		crews[idx].lastDest = f.Dest
		crews[idx].lastArr = f.SchedDep + f.Duration
		crews[idx].has = true
	}
	return flights, cfg
}

// checkInvariants 校验任一时刻全部航班结论满足的全局不变量。
// 已实际起飞（实际起飞时刻不大于 now）的航班是历史事实，不参与间隔与跨度校验。
func checkInvariants(t *testing.T, e *Engine, flights []Flight, cfg Config, now int) {
	t.Helper()
	res := map[string]Result{}
	for _, f := range flights {
		r, err := e.Query(f.ID)
		if err != nil {
			t.Fatalf("Query(%s): %v", f.ID, err)
		}
		res[f.ID] = r
		if r.Status == StatusCancelled {
			continue
		}
		if cf := cfg.Curfews[f.Origin]; cf.Contains(r.Dep) {
			t.Fatalf("%s 在宵禁区间 %v 内起飞 %d", f.ID, cf, r.Dep)
		}
		if cf := cfg.Curfews[f.Dest]; cf.Contains(r.Arr) {
			t.Fatalf("%s 在宵禁区间 %v 内到达 %d", f.ID, cf, r.Arr)
		}
	}
	chains := map[string][]Flight{}
	for _, f := range flights {
		chains["A"+f.Aircraft] = append(chains["A"+f.Aircraft], f)
		chains["C"+f.Crew] = append(chains["C"+f.Crew], f)
	}
	for key, ch := range chains {
		slices.SortFunc(ch, func(x, y Flight) int { return cmp.Compare(x.SchedDep, y.SchedDep) })
		gap := cfg.MinTurnaround
		if key[0] == 'C' {
			gap = cfg.MinConnection
		}
		var prev *Result
		dutyStart := -1
		for _, f := range ch {
			r := res[f.ID]
			if r.Status == StatusCancelled {
				continue
			}
			if prev != nil && r.Dep > now && r.Dep < prev.Arr+gap {
				t.Fatalf("%s 与链上前段间隔 %d 小于最小值 %d", f.ID, r.Dep-prev.Arr, gap)
			}
			if key[0] == 'C' {
				if dutyStart < 0 {
					dutyStart = r.Dep
				}
				if r.Dep > now && r.Arr-dutyStart > cfg.MaxDuty {
					t.Fatalf("%s 机组跨度 %d 超过上限 %d", f.ID, r.Arr-dutyStart, cfg.MaxDuty)
				}
			}
			cur := res[f.ID]
			prev = &cur
		}
	}
}

// 与独立朴素对照模型比对：随机航班表 + 随机注入序列，逐步比对并打印日志。
func TestRandomizedAgainstNaive(t *testing.T) {
	for seed := 0; seed < 600; seed++ {
		r := rand.New(rand.NewSource(int64(seed)))
		flights, cfg := genCase(r)
		eng, err := NewEngine(flights, cfg)
		if err != nil {
			t.Fatalf("seed=%d NewEngine: %v", seed, err)
		}
		nv := newNaive(flights, cfg)
		snapshot := func() map[string]Result {
			m := map[string]Result{}
			for _, f := range flights {
				m[f.ID] = mustQuery(t, eng, f.ID)
			}
			return m
		}
		compare := func(step string, before map[string]Result) {
			t.Helper()
			for _, f := range flights {
				got := mustQuery(t, eng, f.ID)
				want := nv.res[f.ID]
				if got != want {
					t.Fatalf("seed=%d %s 航班 %s 结论不一致: 引擎 %+v vs 朴素 %+v", seed, step, f.ID, got, want)
				}
				if before[f.ID] != got {
					t.Logf("seed=%d %s 航班 %s: %v -> %v", seed, step, f.ID, before[f.ID], got)
				}
			}
		}
		compare("初始", snapshot())
		checkInvariants(t, eng, flights, cfg, 0)

		type injRec struct {
			flight  string
			cancel  bool
			minutes int
		}
		active := map[InjectionID]injRec{}
		now := 0
		for step := 0; step < 60; step++ {
			now += r.Intn(25)
			before := snapshot()
			f := flights[r.Intn(len(flights))]
			var desc string
			switch r.Intn(4) {
			case 0, 1:
				m := r.Intn(150)
				id, err := eng.InjectDelay(f.ID, m, now)
				desc = fmt.Sprintf("InjectDelay(%s,%d)", f.ID, m)
				if err != nil {
					desc += " 拒绝:" + err.Error()
				} else {
					active[id] = injRec{flight: f.ID, minutes: m}
					nv.injectDelay(f.ID, m, now)
				}
			case 2:
				id, err := eng.InjectCancel(f.ID, now)
				desc = fmt.Sprintf("InjectCancel(%s)", f.ID)
				if err != nil {
					desc += " 拒绝:" + err.Error()
				} else {
					active[id] = injRec{flight: f.ID, cancel: true}
					nv.injectCancel(f.ID, now)
				}
			case 3:
				if len(active) == 0 {
					continue
				}
				ids := make([]InjectionID, 0, len(active))
				for id := range active {
					ids = append(ids, id)
				}
				slices.Sort(ids)
				id := ids[r.Intn(len(ids))]
				rec := active[id]
				err := eng.Withdraw(rec.flight, id, now)
				desc = fmt.Sprintf("Withdraw(%s,#%d)", rec.flight, id)
				if err != nil {
					desc += " 拒绝:" + err.Error()
				} else {
					delete(active, id)
					if rec.cancel {
						nv.withdrawCancel(rec.flight, now)
					} else {
						nv.withdrawDelay(rec.flight, rec.minutes, now)
					}
				}
			}
			t.Logf("seed=%d step=%d now=%d op=%s", seed, step, now, desc)
			compare(fmt.Sprintf("step=%d op=%s", step, desc), before)
			checkInvariants(t, eng, flights, cfg, now)
		}
	}
}
