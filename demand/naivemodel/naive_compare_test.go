package naivemodel_test

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/demand"
	"ontology/demand/naivemodel"
)

type op struct {
	kind   int // 0 report, 1 add, 2 lock, 3 unlock, 4 remove
	t      int64
	energy int64
	id     int
	kw     int64
	pri    int
	minOn  int64
	minOff int64
}

type mismatch string

var lastCfgInfo string

func die(format string, args ...any) { panic(mismatch(fmt.Sprintf(format, args...))) }

func runReference(rng *rand.Rand, ops []op) (diff string) {
	defer func() {
		if r := recover(); r != nil {
			if m, ok := r.(mismatch); ok {
				diff = string(m)
				return
			}
			panic(r)
		}
	}()

	contract := int64(80 + rng.Intn(60))
	slip := int64(5 + rng.Intn(20))
	n := int64(2 + rng.Intn(5))
	cfg := demand.Config{
		ContractKW: contract,
		WindowSec:  slip * n,
		SlipSec:    slip,
		MaxPowerKW: 600,
	}
	ncfg := naivemodel.Config{
		ContractKW: contract,
		WindowSec:  cfg.WindowSec,
		SlipSec:    slip,
		MaxPowerKW: 600,
	}
	lastCfgInfo = fmt.Sprintf("contract=%d window=%d slip=%d", contract, cfg.WindowSec, slip)
	ctrl, err := demand.New(cfg)
	if err != nil {
		t2(err)
	}
	ref := naivemodel.New(ncfg)

	for i, o := range ops {
		switch o.kind {
		case 0:
			r1, e1 := ctrl.Report(o.t, o.energy)
			r2, e2 := ref.Report(o.t, o.energy)
			compareErr(i, o, e1, e2)
			if e1 == nil {
				compareResult(i, o, r1, r2)
				comparePeak(i, ctrl, ref)
			}
		case 1:
			e1 := ctrl.AddLoad(o.t, demand.LoadSpec{
				ID: o.id, RatedKW: o.kw, Priority: o.pri,
				MinOnSec: o.minOn, MinOffSec: o.minOff,
			})
			e2 := ref.AddLoad(o.t, naivemodel.LoadSpec{
				ID: o.id, RatedKW: o.kw, Priority: o.pri,
				MinOnSec: o.minOn, MinOffSec: o.minOff,
			})
			compareErr(i, o, e1, e2)
		case 2:
			compareErr(i, o, ctrl.LockLoad(o.t, o.id), ref.LockLoad(o.t, o.id))
		case 3:
			compareErr(i, o, ctrl.UnlockLoad(o.t, o.id), ref.UnlockLoad(o.t, o.id))
		case 4:
			compareErr(i, o, ctrl.RemoveLoad(o.t, o.id), ref.RemoveLoad(o.t, o.id))
		}
	}
	return ""
}

func t2(err error) {
	if err != nil {
		die("setup: %v", err)
	}
}

func compareErr(i int, o op, e1, e2 error) {
	k1, k2 := errKindOf(e1), refErrKindOf(e2)
	if k1 != k2 {
		die("op#%d %+v 错误类别不一致: controller=%v(%v) naive=%v(%v)",
			i, o, k1, e1, k2, e2)
	}
}

func compareResult(i int, o op, r1 *demand.EvalResult, r2 *naivemodel.Result) {
	if r1.At != r2.At {
		die("op#%d At 不一致 %d vs %d", i, r1.At, r2.At)
	}
	if r1.StillExceed != r2.StillExceed {
		die("op#%d StillExceed 不一致 %v vs %v (op=%+v)",
			i, r1.StillExceed, r2.StillExceed, o)
	}
	a1 := make([][2]int, 0, len(r1.Actions))
	for _, a := range r1.Actions {
		k := 0
		if a.Kind == demand.ActionShed {
			k = 1
		} else if a.Kind == demand.ActionRestore {
			k = 2
		}
		a1 = append(a1, [2]int{k, a.LoadID})
	}
	a2 := make([][2]int, 0, len(r2.Actions))
	for _, a := range r2.Actions {
		a2 = append(a2, [2]int{a.Kind, a.LoadID})
	}
	sortPairs(a1)
	sortPairs(a2)
	if len(a1) != len(a2) {
		die("op#%d 动作数量不一致 ctrl=%v naive=%v (op=%+v)", i, a1, a2, o)
	}
	for j := range a1 {
		if a1[j] != a2[j] {
			die("op#%d 动作不一致 ctrl=%v naive=%v (op=%+v)", i, a1, a2, o)
		}
	}
}

func sortPairs(a [][2]int) {
	sort.Slice(a, func(p, q int) bool {
		if a[p][0] != a[q][0] {
			return a[p][0] < a[q][0]
		}
		return a[p][1] < a[q][1]
	})
}

func comparePeak(i int, c *demand.Controller, m *naivemodel.Model) {
	p1, p2 := c.Peak(), m.Peak()
	if (p1 == nil) != (p2 == nil) {
		die("op#%d peak nil 不一致 %+v vs %+v", i, p1, p2)
	}
	if p1 == nil {
		return
	}
	if p1.EndAt != p2.EndAt {
		die("op#%d peak EndAt 不一致 %d vs %d", i, p1.EndAt, p2.EndAt)
	}
	if p1.PowerKW.Rat().Cmp(p2.PowerKW) != 0 {
		die("op#%d peak power 不一致 %s vs %s",
			i, p1.PowerKW.Rat().String(), p2.PowerKW.String())
	}
}

func errKindOf(e error) int {
	if e == nil {
		return 0
	}
	if x, ok := e.(*demand.Error); ok {
		return int(x.Kind)
	}
	return -1
}

func refErrKindOf(e error) int {
	if e == nil {
		return 0
	}
	if x, ok := e.(*naivemodel.MError); ok {
		return int(x.Kind)
	}
	return -1
}

func genOps(rng *rand.Rand) []op {
	var ops []op
	t := int64(0)
	nextID := 1

	nLoads := 2 + rng.Intn(6)
	for j := 0; j < nLoads; j++ {
		ops = append(ops, op{
			kind: 1, t: 0, id: nextID,
			kw:     int64(5 + rng.Intn(40)),
			pri:    1 + rng.Intn(4),
			minOn:  int64(rng.Intn(20)),
			minOff: int64(rng.Intn(20)),
		})
		nextID++
	}

	nReports := 60 + rng.Intn(120)
	for j := 0; j < nReports; j++ {
		step := int64(1 + rng.Intn(12))
		nt := t + step
		energy := int64(rng.Intn(640)) * step
		// 少量非法上报
		switch rng.Intn(12) {
		case 0:
			nt = t // 时刻不严格递增
		case 1:
			energy = -1 // 负用电量
		case 2:
			energy = 601*step + 1 // 超物理上限
		}
		ops = append(ops, op{kind: 0, t: nt, energy: energy})
		if nt > t {
			t = nt
		}

		// 运维穿插（仅在时刻合法推进后）
		if rng.Intn(4) == 0 {
			id := 1 + rng.Intn(nextID-1)
			switch rng.Intn(4) {
			case 0:
				ops = append(ops, op{kind: 2, t: t, id: id})
			case 1:
				ops = append(ops, op{kind: 3, t: t, id: id})
			case 2:
				ops = append(ops, op{kind: 4, t: t, id: id})
			case 3:
				ops = append(ops, op{
					kind: 4 + 0, t: t,
				})
				// 新增一个不冲突编号
				ops[len(ops)-1] = op{
					kind: 1, t: t, id: nextID,
					kw:     int64(5 + rng.Intn(40)),
					pri:    1 + rng.Intn(4),
					minOn:  int64(rng.Intn(20)),
					minOff: int64(rng.Intn(20)),
				}
				nextID++
			}
		}
	}
	return ops
}

func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(1); seed <= 800; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := genOps(rng)
		if diff := runReference(rng, ops); diff != "" {
			for j, o := range ops {
				t.Logf("seed=%d op#%d %+v", seed, j, o)
			}
			t.Fatalf("seed=%d %s 分歧: %s", seed, lastCfgInfo, diff)
		}
	}
}
