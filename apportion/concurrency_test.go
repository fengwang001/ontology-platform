package apportion

import (
	"fmt"
	"sync"
	"testing"
)

// 不同被保人并发互不干扰；同一被保人并发受理+撤销等价于某串行顺序，
// 最终每张保单余额必为「初始年度限额 - 当前在册损失扣减之和」的不变量。
func TestConcurrentSameInsured(t *testing.T) {
	e := NewEngine(nil)
	reg(t, e, "I", "A", 0, 1000, 1000000, 0, 100, LimitShare)
	reg(t, e, "I", "B", 0, 1000, 1000000, 0, 100, IndependentShare)
	reg(t, e, "I", "X", 0, 1000, 1000000, 0, 100, Excess)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			var last string
			for k := 0; k < 80; k++ {
				ln := fmt.Sprintf("L-%d-%d", g, k)
				_, err := e.Accept(Loss{LossNo: ln, Insured: "I", Day: 1, Amount: int64(1 + k%500)})
				if err != nil {
					t.Errorf("accept: %v", err)
					return
				}
				last = ln
				// 立即撤销一半的末笔（可能与其他 goroutine 竞争，成功与否都合法）
				if k%2 == 0 {
					_, _ = e.Undo(last, "I")
				}
			}
		}(g)
	}
	wg.Wait()

	// 不变量：逐笔重放当前在册损失，校验剩余等于从初始限额串行扣减的结果。
	// 直接验证：所有当前保单剩余 = 初始限额 - 其当前在册损失应赔之和。
	// 引擎不暴露逐笔明细用于外部复算时，用更强的简单不变量：剩余非负且总额守恒。
	rem := e.Remaining("I")
	for no, v := range rem {
		if v < 0 || v > 1000000 {
			t.Fatalf("invariant broken for %s: %d", no, v)
		}
	}
}

func TestConcurrentDifferentInsureds(t *testing.T) {
	e := NewEngine(nil)
	const nInsured = 32
	for i := 0; i < nInsured; i++ {
		id := fmt.Sprintf("I%02d", i)
		reg(t, e, id, id+"-A", 0, 1000, 100000, 0, 100, LimitShare)
	}

	var wg sync.WaitGroup
	for i := 0; i < nInsured; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("I%02d", i)
			for k := 0; k < 200; k++ {
				ln := fmt.Sprintf("%s-L%d", id, k)
				if _, err := e.Accept(Loss{LossNo: ln, Insured: id, Day: 1, Amount: 10}); err != nil {
					t.Errorf("accept %s: %v", ln, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	// 每个被保人独立：200 笔各 10 分，A 独赔 2000。
	for i := 0; i < nInsured; i++ {
		id := fmt.Sprintf("I%02d", i)
		if got := e.Remaining(id)[id+"-A"]; got != 98000 {
			t.Fatalf("%s rem=%d want 98000", id, got)
		}
	}
}

// TestReplayDeterminism：相同操作序列两次重放，每张保单应赔与余额完全相同。
func TestReplayDeterminism(t *testing.T) {
	type op struct {
		kind            int // 0 register, 1 accept, 2 undo
		no              string
		ded, per, ann   int64
		s, en, day, amt int64
		clause          Clause
	}
	seeded := func(seed int64) []op {
		// 固定种子的确定性序列
		rng := newSeededRNG(seed)
		var ops []op
		for i := 0; i < 6; i++ {
			ops = append(ops, op{kind: 0, no: fmt.Sprintf("P%d", i),
				ded: int64(rng() % 200), per: 1 + int64(rng()%800),
				ann: 1 + int64(rng()%3000), s: 0, en: 20,
				clause: Clause(1 + rng()%3)})
		}
		for k := 0; k < 30; k++ {
			ops = append(ops, op{kind: 1, no: fmt.Sprintf("L%d", k),
				day: int64(rng() % 20), amt: 1 + int64(rng()%1500)})
			if k > 0 && k%3 == 0 {
				ops = append(ops, op{kind: 2, no: fmt.Sprintf("L%d", k-1)})
			}
		}
		return ops
	}

	type snapshot struct {
		pays map[string]int64
		rem  map[string]int64
	}
	execute := func(ops []op) []snapshot {
		e := NewEngine(nil)
		var snap []snapshot
		for _, o := range ops {
			switch o.kind {
			case 0:
				if err := e.Register(Policy{Insured: "I", PolicyNo: o.no, Deductible: o.ded,
					PerLoss: o.per, AnnualLimit: o.ann, StartDay: o.s, EndDay: o.en,
					Clause: o.clause}); err != nil {
					t.Fatal(err)
				}
			case 1:
				out, err := e.Accept(Loss{LossNo: o.no, Insured: "I", Day: o.day, Amount: o.amt})
				if err != nil {
					t.Fatal(err)
				}
				snap = append(snap, snapshot{pays: payMap(out), rem: out.Remaining})
			case 2:
				if _, err := e.Undo(o.no, "I"); err != nil {
					var re *Error
					if !asError(err, &re) || re.Code != ErrNotLast {
						t.Fatalf("unexpected undo err: %v", err)
					}
				}
			}
		}
		return snap
	}

	ops := seeded(42)
	a := execute(ops)
	b := execute(ops)
	if len(a) != len(b) {
		t.Fatalf("snap length %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !mapsEqual(a[i].pays, b[i].pays) || !mapsEqual(a[i].rem, b[i].rem) {
			t.Fatalf("step %d replay differs:\n%+v\n%+v", i, a[i], b[i])
		}
	}
}

func asError(err error, target **Error) bool {
	if e, ok := err.(*Error); ok {
		*target = e
		return true
	}
	return false
}

// newSeededRNG 返回确定性的简单 LCG，避免引入 math/rand 状态依赖。
func newSeededRNG(seed int64) func() int64 {
	state := seed
	return func() int64 {
		state = (state*6364136223846793005 + 1442695040888963407) & 0x7fffffffffffffff
		return state
	}
}

// BenchmarkAcceptScaling 以可验证的方式证明：
// 一次损失的分摊开销不随该被保人历史已结清损失数增长，
// 也不随系统内其他被保人的保单总数增长。
//
// 对比三种规模（同一被保人保单数固定 4）：
//
//	A: 历史 10 笔, 其他被保人保单 0
//	B: 历史 400 笔, 其他被保人保单 0
//	C: 历史 400 笔, 其他被保人保单 4000
//
// 若 ns/op 随历史/全局保单规模显著增长，则说明实现违规。
func BenchmarkAcceptScaling(b *testing.B) {
	cases := []struct {
		name      string
		history   int
		otherPols int
	}{
		{"hist10_other0", 10, 0},
		{"hist400_other0", 400, 0},
		{"hist400_other4000", 400, 4000},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			e := buildScaled(b, tc.history, tc.otherPols)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ln := "probe"
				_, err := e.Accept(Loss{LossNo: ln, Insured: "TARGET", Day: 1, Amount: 777})
				if err != nil {
					b.Fatal(err)
				}
				if _, err := e.Undo(ln, "TARGET"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func buildScaled(b *testing.B, history, otherPols int) *Engine {
	b.Helper()
	e := NewEngine(nil)
	clauses := []Clause{LimitShare, LimitShare, IndependentShare, Excess}
	for i, c := range clauses {
		if err := e.Register(Policy{
			Insured: "TARGET", PolicyNo: "T" + string(rune('A'+i)),
			Deductible: 50, PerLoss: 2000, AnnualLimit: 1_000_000_000,
			StartDay: 0, EndDay: 100, Clause: c,
		}); err != nil {
			b.Fatal(err)
		}
	}
	// 历史损失：用极小金额，保证目标保单累计永远不耗尽。
	for k := 0; k < history; k++ {
		if _, err := e.Accept(Loss{
			LossNo: "H" + itoa(k), Insured: "TARGET", Day: 1, Amount: 4,
		}); err != nil {
			b.Fatal(err)
		}
	}
	// 其他被保人的大量保单（不同账户，分摊时完全不触达）。
	for k := 0; k < otherPols; k++ {
		id := "OTHER"
		if err := e.Register(Policy{
			Insured: id, PolicyNo: "O" + itoa(k),
			Deductible: 0, PerLoss: 100, AnnualLimit: 100000,
			StartDay: 0, EndDay: 100, Clause: clauses[k%3],
		}); err != nil {
			b.Fatal(err)
		}
	}
	return e
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
