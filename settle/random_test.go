package settle

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/catalog"
	"ontology/fund"
)

// opKind 操作类型。
type opKind int

const (
	opAddPerson opKind = iota
	opSetAid
	opSettle
	opReverse
)

type op struct {
	kind   opKind
	now    int64
	id     string
	person string
	year   int
	on     bool
	items  []Item
}

var codes = []string{"jia", "yi", "yi2", "bing", "missing"}

func genOps(rng *rand.Rand) ([]op, params) {
	par := params{
		d:    rng.Int63n(200000),
		s1:   1 + rng.Int63n(1_000_000),
		s2:   0,
		r1:   rng.Int63n(101),
		r2:   rng.Int63n(101),
		r3:   rng.Int63n(101),
		capF: rng.Int63n(2_000_000),
		d2:   rng.Int63n(200000),
		rd:   rng.Int63n(101),
		ra:   rng.Int63n(101),
		capA: rng.Int63n(500000),
	}
	par.s2 = par.s1 + 1 + rng.Int63n(4_000_000)

	n := 30 + rng.Intn(60)
	ops := make([]op, 0, n)
	now := int64(0)
	np := 1 + rng.Intn(4)
	seq := 0
	for i := 0; i < n; i++ {
		roll := rng.Intn(100)
		person := fmt.Sprintf("p%d", rng.Intn(np+1)) // 偶尔引用不存在的人
		switch {
		case roll < 15:
			ops = append(ops, op{kind: opAddPerson, person: person})
		case roll < 35:
			now += int64(rng.Intn(5))
			ops = append(ops, op{kind: opSetAid, now: now, person: person, on: rng.Intn(2) == 0})
		case roll < 85:
			// 少量故意回退/非法
			useNow := now
			if rng.Intn(10) == 0 {
				useNow = now - int64(1+rng.Intn(3))
			} else {
				now += int64(rng.Intn(4))
				useNow = now
			}
			seq++
			k := 1 + rng.Intn(4)
			items := make([]Item, k)
			for j := range items {
				items[j] = Item{
					Code:  codes[rng.Intn(len(codes))],
					Price: 1 + rng.Int63n(20000),
					Qty:   1 + rng.Int63n(10),
				}
			}
			year := 1 + rng.Intn(4)
			id := fmt.Sprintf("id%d", seq)
			if rng.Intn(12) == 0 {
				id = "id1" // 潜在重复
			}
			ops = append(ops, op{kind: opSettle, now: useNow, id: id, person: person, year: year, items: items})
		default:
			now += int64(rng.Intn(4))
			id := fmt.Sprintf("id%d", 1+rng.Intn(seq+2))
			ops = append(ops, op{kind: opReverse, now: now, id: id})
		}
	}
	return ops, par
}

func buildEngine(par params) *Engine {
	e := New(par.d, par.s1, par.s2, par.r1, par.r2, par.r3, par.capF, par.d2, par.rd, par.ra, par.capA)
	_ = e.AddItem("jia", catalog.Jia, 0, 10000)
	_ = e.AddItem("yi", catalog.Yi, 10, 5000)
	_ = e.AddItem("yi2", catalog.Yi, 33, 0)
	_ = e.AddItem("bing", catalog.Bing, 0, 0)
	return e
}

// runPair 对同一操作序列分别驱动引擎与朴素模拟，逐步比对。
func runPair(t *testing.T, seed int64, ops []op, par params) {
	t.Helper()
	e := buildEngine(par)
	nv := newNaive(par)

	for i, o := range ops {
		switch o.kind {
		case opAddPerson:
			ge := e.AddPerson(o.person)
			ne := nv.addPerson(o.person)
			if !sameErr(ge, ne) {
				t.Fatalf("seed=%d op=%d AddPerson err engine=%v naive=%v", seed, i, ge, ne)
			}
		case opSetAid:
			ge := e.SetAid(o.now, o.person, o.on)
			ne := nv.setAid(o.now, o.person, o.on)
			if !sameErr(ge, ne) {
				t.Fatalf("seed=%d op=%d SetAid err engine=%v naive=%v", seed, i, ge, ne)
			}
		case opSettle:
			gr, ge := e.Settle(o.now, o.id, o.person, o.year, o.items)
			nr, ne := nv.settle(o.now, o.id, o.person, o.year, o.items)
			if !sameErr(ge, ne) {
				t.Fatalf("seed=%d op=%d Settle err engine=%v naive=%v; op=%+v", seed, i, ge, ne, o)
			}
			if ge == nil {
				if gr != nr {
					t.Fatalf("seed=%d op=%d Settle result\nengine=%+v\nnaive =%+v", seed, i, gr, nr)
				}
				// 不变量：A=F1+F2+F3+self；非负；封顶。
				if gr.A != gr.F1+gr.F2+gr.F3+gr.Self || gr.F1 < 0 || gr.F2 < 0 || gr.F3 < 0 || gr.Self < 0 {
					t.Fatalf("seed=%d op=%d identity violated %+v", seed, i, gr)
				}
				acc := e.Acc(o.person, o.year)
				nacc := nv.acc[o.person][o.year]
				if acc != nacc {
					t.Fatalf("seed=%d op=%d acc engine=%+v naive=%+v", seed, i, acc, nacc)
				}
				if acc.DU > par.d || acc.F > par.capF || acc.Q > par.capA {
					t.Fatalf("seed=%d op=%d cap invariant acc=%+v par=%+v", seed, i, acc, par)
				}
				if t.Failed() {
					return
				}
			}
		case opReverse:
			ge := e.Reverse(o.now, o.id)
			ne := nv.reverse(o.now, o.id)
			if !sameErr(ge, ne) {
				t.Fatalf("seed=%d op=%d Reverse err engine=%v naive=%v id=%s", seed, i, ge, ne, o.id)
			}
			if ge == nil {
				r := nv.recs[o.id]
				acc := e.Acc(r.person, r.year)
				nacc := nv.acc[r.person][r.year]
				if acc != nacc {
					t.Fatalf("seed=%d op=%d post-reverse acc engine=%+v naive=%+v", seed, i, acc, nacc)
				}
			}
		}
	}
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) || errors.Is(b, a) || (a == nil && b == nil)
}

// TestRandomAgainstNaive 1500 组随机操作序列逐分对照。
func TestRandomAgainstNaive(t *testing.T) {
	const N = 1500
	for seed := int64(1); seed <= N; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops, par := genOps(rng)
		if seed <= 3 {
			t.Logf("seed=%d ops=%d par=%+v (输入/输出/判定依据见失败信息)", seed, len(ops), par)
		}
		runPair(t, seed, ops, par)
	}
	t.Logf("random: %d sequences matched naive accumulator fen-by-fen", N)
}

// TestTouchedHistoryIndependent touched：Settle=0，Reverse≤1，与历史笔数无关。
func TestTouchedHistoryIndependent(t *testing.T) {
	for _, n := range []int{10, 10000} {
		e := buildEngine(params{d: 100, s1: 1 << 40, s2: 1 << 41, r1: 0, r2: 0, r3: 0,
			capF: 1e13, d2: 1e13, rd: 0, ra: 0, capA: 1e13})
		if err := e.AddPerson("p"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			items := []Item{{Code: "jia", Price: 1, Qty: 1}}
			if _, err := e.Settle(int64(i+1), fmt.Sprintf("h%d", i), "p", 1, items); err != nil {
				t.Fatalf("n=%d settle %d: %v", n, i, err)
			}
			if got := e.Touched(); got != 0 {
				t.Fatalf("n=%d settle touched=%d want 0", n, got)
			}
		}
		// 冲正最后一笔只触达 1 份历史。
		if err := e.Reverse(int64(n+1), fmt.Sprintf("h%d", n-1)); err != nil {
			t.Fatalf("n=%d reverse: %v", n, err)
		}
		if got := e.Touched(); got != 1 {
			t.Fatalf("n=%d reverse touched=%d want <=1", n, got)
		}
		t.Logf("touched: history=%d Settle=0 Reverse=1", n)
	}
}

var _ = fund.Acc{}
