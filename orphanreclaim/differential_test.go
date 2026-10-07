package orphanreclaim

import (
	"math/rand"
	"testing"
)

type diffOp struct {
	kind int // 0 create 1 add 2 remove 3 advance 4 tick
	a, b string
	lt   string
	t    int64
}

func generateDiffOps(rng *rand.Rand, n int) []diffOp {
	linkTypes := []string{"I", "J1", "J2", "X", "Y"}
	var objs []string
	var ops []diffOp
	ensure := func(count int) {
		for len(objs) < count {
			id := "o" + itoa(len(objs))
			objs = append(objs, id)
			ops = append(ops, diffOp{kind: 0, a: id})
		}
	}
	ensure(6)

	now := int64(0)
	for i := 0; i < n; i++ {
		switch rng.Intn(10) {
		case 0:
			ensure(len(objs) + 1 + rng.Intn(3))
		case 1, 2, 3, 4, 5:
			src := objs[rng.Intn(len(objs))]
			tgt := objs[rng.Intn(len(objs))]
			ops = append(ops, diffOp{kind: 1, a: src, b: tgt, lt: linkTypes[rng.Intn(5)]})
		case 6, 7:
			src := objs[rng.Intn(len(objs))]
			tgt := objs[rng.Intn(len(objs))]
			ops = append(ops, diffOp{kind: 2, a: src, b: tgt, lt: linkTypes[rng.Intn(5)]})
		case 8:
			now += int64(rng.Intn(7)) // 反复穿越 10 / 4 宽限边界
			ops = append(ops, diffOp{kind: 4, t: now})
		case 9:
			ops = append(ops, diffOp{kind: 3})
		}
	}
	ops = append(ops, diffOp{kind: 3})
	return ops
}

// 正式引擎与逐条扫描全部入边的朴素模型并排重放同一操作序列，
// 每 25 条操作及终态逐字段比对（存活集、代数、判定时刻、入/出边、两代队列）。
func TestRandomDifferentialAgainstNaive(t *testing.T) {
	cfg := testConfig()
	for seed := int64(1); seed <= 24; seed++ {
		ops := generateDiffOps(rand.New(rand.NewSource(seed)), 1500)

		c := &fakeClock{}
		engine, err := New(cfg, c.now, nil, nopLogger{})
		if err != nil {
			t.Fatal(err)
		}
		naive := NewNaive(cfg, 0)

		for i, o := range ops {
			switch o.kind {
			case 0:
				engine.CreateObject(o.a)
				naive.CreateObject(o.a)
			case 1:
				errE := engine.AddLink(o.a, o.b, o.lt)
				errN := naive.AddLink(o.a, o.b, o.lt)
				if (errE == nil) != (errN == nil) {
					t.Fatalf("seed=%d op=%d add error mismatch: engine=%v naive=%v", seed, i, errE, errN)
				}
			case 2:
				errE := engine.RemoveLink(o.a, o.b, o.lt)
				errN := naive.RemoveLink(o.a, o.b, o.lt)
				if (errE == nil) != (errN == nil) {
					t.Fatalf("seed=%d op=%d remove error mismatch: engine=%v naive=%v", seed, i, errE, errN)
				}
			case 3:
				engine.Advance()
				naive.Advance()
			case 4:
				c.t = o.t
				naive.SetTime(o.t)
			}
			if i%25 == 0 || i == len(ops)-1 {
				cmpSnapshots(t, "seed="+itoa(int(seed))+" op="+itoa(i),
					engine.Snapshot(), naive.Snapshot())
			}
		}
	}
}

func itoa64(n int64) string { return itoa(int(n)) }
