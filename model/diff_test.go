package model

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"reflect"
	"testing"

	"ontology/van"
)

// classify 把错误归一为可比较的标签（含拒绝原因）。
func classify(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, van.ErrInvalid):
		return "invalid"
	case errors.Is(err, van.ErrDuplicate):
		return "duplicate"
	case errors.Is(err, van.ErrStopPassed):
		return "stop_passed"
	case errors.Is(err, van.ErrOrderError):
		return "unload_order"
	case errors.Is(err, van.ErrProcessed):
		return "processed"
	case errors.Is(err, van.ErrNotFound):
		return "not_found"
	}
	var re *van.RejectError
	if errors.As(err, &re) {
		return "reject:" + re.Reason.String()
	}
	return "other:" + err.Error()
}

type snapshot struct {
	loc     map[int]int
	rem     []van.Remaining
	arrived int
}

func (m *Naive) snap() snapshot {
	loc := map[int]int{}
	for id := range m.board {
		loc[id] = m.zoneOf[id]
	}
	return snapshot{loc: loc, rem: m.Remaining(), arrived: m.arrived}
}

type opKind int

const (
	opLoad opKind = iota
	opBatch
	opUnload
	opQuery
)

type op struct {
	kind  opKind
	cargo van.Cargo
	batch []van.Cargo
	stop  int
	query int // 0=Remaining, 1=Location, 2=ArrivedStop
	locID int
}

func randomCargo(rng *rand.Rand, id, stopMax int) van.Cargo {
	return van.Cargo{
		ID:     id,
		Weight: rng.Intn(8) + 1,
		Volume: rng.Intn(8) + 1,
		Stop:   rng.Intn(stopMax) + 1,
		Kind:   van.Category(rng.Intn(4)),
	}
}

// runDiff 对同一随机操作序列同时驱动两套实现，逐步比对结果与完整状态。
// 日志逐行打印输入、输出与判定依据到 logw（同时在失败时由 testing 输出）。
func runDiff(t *testing.T, rng *rand.Rand, logw io.Writer, steps, zones, stopMax int) {
	specs := make([]van.ZoneSpec, zones)
	for i := range specs {
		specs[i] = van.ZoneSpec{
			WeightLimit: rng.Intn(12) + 4,
			VolumeLimit: rng.Intn(12) + 4,
		}
	}
	fmt.Fprintf(logw, "=== 新序列 specs=%v 步数=%d ===\n", specs, steps)
	svc := van.New(specs)
	nav := New(specs)

	nextID := 1
	known := map[int]bool{} // 在两套系统中都“尝试过编号”的集合（含已卸货），用于 Location 对比

	checkState := func(step int, tag string) {
		ns := nav.snap()
		rem := svc.Remaining()
		if !reflect.DeepEqual(rem, ns.rem) {
			t.Fatalf("[step %d %s] Remaining 不一致 svc=%v naive=%v", step, tag, rem, ns.rem)
		}
		if svc.ArrivedStop() != ns.arrived {
			t.Fatalf("[step %d %s] arrived 不一致 svc=%d naive=%d", step, tag, svc.ArrivedStop(), ns.arrived)
		}
		for id := range known {
			z1, e1 := svc.Location(id)
			z2, e2 := nav.Location(id)
			if classify(e1) != classify(e2) || z1 != z2 {
				t.Fatalf("[step %d %s] Location(%d) 不一致 svc=(%d,%v) naive=(%d,%v)",
					step, tag, id, z1, e1, z2, e2)
			}
		}
	}

	for step := 0; step < steps; step++ {
		k := opKind(rng.Intn(4))
		var o op
		o.kind = k
		switch k {
		case opLoad:
			// 30% 概率复用已用编号以制造重复/重装场景
			if len(known) > 0 && rng.Intn(10) < 3 {
				ids := make([]int, 0, len(known))
				for id := range known {
					ids = append(ids, id)
				}
				c := randomCargo(rng, ids[rng.Intn(len(ids))], stopMax)
				o.cargo = c
			} else {
				o.cargo = randomCargo(rng, nextID, stopMax)
				nextID++
			}
			known[o.cargo.ID] = true
			// 小概率注入非法值
			if rng.Intn(10) == 0 {
				o.cargo.Weight = 0
			}
		case opBatch:
			n := rng.Intn(3) + 1
			for i := 0; i < n; i++ {
				c := randomCargo(rng, nextID, stopMax)
				nextID++
				known[c.ID] = true
				o.batch = append(o.batch, c)
			}
			if rng.Intn(5) == 0 && len(o.batch) > 1 {
				o.batch[1].ID = o.batch[0].ID // 批内重复
			}
		case opUnload:
			o.stop = rng.Intn(stopMax) + 1
		case opQuery:
			o.query = rng.Intn(3)
			if len(known) > 0 {
				ids := make([]int, 0, len(known))
				for id := range known {
					ids = append(ids, id)
				}
				o.locID = ids[rng.Intn(len(ids))]
			} else {
				o.locID = nextID
			}
		}

		switch k {
		case opLoad:
			z1, e1 := svc.Load(o.cargo)
			z2, e2 := nav.Load(o.cargo)
			fmt.Fprintf(logw, "[%d] Load %s -> svc(分区%d, %s) naive(分区%d, %s)\n",
				step, o.cargo, z1, classify(e1), z2, classify(e2))
			if z1 != z2 || classify(e1) != classify(e2) {
				t.Fatalf("[%d] Load 不一致: svc=(%d,%v) naive=(%d,%v) cargo=%v",
					step, z1, e1, z2, e2, o.cargo)
			}
		case opBatch:
			r1, e1 := svc.LoadBatch(o.batch)
			r2, e2 := nav.LoadBatch(o.batch)
			fmt.Fprintf(logw, "[%d] LoadBatch %v -> svc(%v, %s) naive(%v, %s)\n",
				step, o.batch, r1, classify(e1), r2, classify(e2))
			if classify(e1) != classify(e2) {
				t.Fatalf("[%d] LoadBatch 错误不一致: svc=%v naive=%v", step, e1, e2)
			}
			if e1 == nil && !reflect.DeepEqual(r1, r2) {
				t.Fatalf("[%d] LoadBatch 结果不一致: svc=%v naive=%v", step, r1, r2)
			}
			// 失败下标也需一致
			var be1 *van.BatchError
			if errors.As(e1, &be1) {
				idx2, _, ok := IsBatchError(e2)
				if !ok || be1.Index != idx2 {
					t.Fatalf("[%d] LoadBatch 失败下标不一致 svc=%d naive=%d", step, be1.Index, idx2)
				}
			}
		case opUnload:
			i1, e1 := svc.Unload(o.stop)
			i2, e2 := nav.Unload(o.stop)
			fmt.Fprintf(logw, "[%d] Unload(%d) -> svc(%v, %s) naive(%v, %s)\n",
				step, o.stop, i1, classify(e1), i2, classify(e2))
			if classify(e1) != classify(e2) || !reflect.DeepEqual(i1, i2) {
				t.Fatalf("[%d] Unload(%d) 不一致: svc=(%v,%v) naive=(%v,%v)",
					step, o.stop, i1, e1, i2, e2)
			}
		case opQuery:
			switch o.query {
			case 0:
				r1, r2 := svc.Remaining(), nav.Remaining()
				fmt.Fprintf(logw, "[%d] Query Remaining -> svc=%v naive=%v\n", step, r1, r2)
			case 1:
				z1, e1 := svc.Location(o.locID)
				z2, e2 := nav.Location(o.locID)
				fmt.Fprintf(logw, "[%d] Query Location(%d) -> svc=(%d,%s) naive=(%d,%s)\n",
					step, o.locID, z1, classify(e1), z2, classify(e2))
			case 2:
				a1, a2 := svc.ArrivedStop(), nav.ArrivedStop()
				fmt.Fprintf(logw, "[%d] Query Arrived -> svc=%d naive=%d\n", step, a1, a2)
			}
		}
		checkState(step, "post")
	}
	fmt.Fprintln(logw, "=== 序列结束，状态一致 ===")
}

func TestDifferentialRandom(t *testing.T) {
	logPath := os.Getenv("DIFF_LOG")
	var logw io.Writer = io.Discard
	if logPath != "" {
		f, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		logw = f
	}
	// 固定种子保证“相同操作序列重放得到相同结果”，同时覆盖大量随机序列。
	for seed := int64(1); seed <= 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		zones := rng.Intn(4) + 1
		fmt.Fprintf(logw, "seed=%d zones=%d\n", seed, zones)
		runDiff(t, rng, logw, 120, zones, 4)
	}
}

// TestDeterministicReplay 相同操作序列重放两次，装载位置必须完全相同。
func TestDeterministicReplay(t *testing.T) {
	specs := []van.ZoneSpec{
		{WeightLimit: 10, VolumeLimit: 10},
		{WeightLimit: 10, VolumeLimit: 10},
		{WeightLimit: 8, VolumeLimit: 12},
	}
	run := func() map[int]int {
		r := rand.New(rand.NewSource(7))
		s := van.New(specs)
		got := map[int]int{}
		for i := 0; i < 60; i++ {
			c := randomCargo(r, i+1, 4)
			if z, err := s.Load(c); err == nil {
				got[c.ID] = z
			}
		}
		return got
	}
	if a, b := run(), run(); !reflect.DeepEqual(a, b) {
		t.Fatalf("重放结果不确定: %v vs %v", a, b)
	}
}
