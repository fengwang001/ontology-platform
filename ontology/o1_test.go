package ontology

import (
	"fmt"
	"testing"
)

type fataler interface {
	Helper()
	Fatalf(string, ...any)
}

func newConstrainedEnvTB(tb fataler, maxAttempts int, hook SettleHook, max int) *testEnv {
	tb.Helper()
	st := NewStore()
	if err := st.RegisterLinkType(LinkType{
		ID:           testLinkType,
		CardinalityA: &Cardinality{Max: max},
	}); err != nil {
		tb.Fatalf("%v", err)
	}
	if err := st.CreateInstance("x"); err != nil {
		tb.Fatalf("%v", err)
	}
	for i := 0; i < 16; i++ {
		if err := st.CreateInstance("y" + itoa(i)); err != nil {
			tb.Fatalf("%v", err)
		}
	}
	return &testEnv{store: st, eng: NewEngine(st, maxAttempts, hook), tA: testLinkType, inst: "x"}
}

// TestCardinalityDecisionO1Evidence 以不依赖额外对外暴露状态的方式给出证据：
// 基数判定路径的“计数器读取次数”只与本次涉及的约束数量 K 成正比，
// 与该实例当前持有的链接总数 N 无关。
func TestCardinalityDecisionO1Evidence(t *testing.T) {
	for _, n := range []int{0, 10, 100, 1000} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			env := newConstrainedEnvTB(t, 1, nil, n+10) // 上限放宽，保证尝试走到基数判定且不被拒
			others := make([]string, 0, n)
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("p%04d", i)
				if err := env.store.CreateInstance(id); err != nil {
					t.Fatal(err)
				}
				others = append(others, id)
			}
			for i, id := range others {
				ok, err := env.store.commitRaider("x", []Op{{TypeID: testLinkType, Side: SideA, Other: id, Add: true}})
				if err != nil || !ok {
					t.Fatalf("seed %d failed: %v", i, err)
				}
			}

			env.store.resetReadStats()
			req := Request{Instance: "x", Baseline: uint64(n), Ops: []Op{
				{TypeID: testLinkType, Side: SideA, Other: "y0", Add: true},
			}}
			res, err := env.eng.Update(req)
			if err != nil || !res.Committed {
				t.Fatalf("want commit: %+v err=%v", res, err)
			}
			counterReads, scans := env.store.readStats()
			// 成功尝试的决策读恒为 2 次计数器表项（审计重读 1 + 锁内权威校验 1），与 N 无关。
			if counterReads != 2 {
				t.Fatalf("N=%d: decision counter reads must be constant 2, got %d", n, counterReads)
			}
			// 全量扫描只服务于审计快照，每次尝试恰好 1 次。
			if scans != 1 {
				t.Fatalf("N=%d: want exactly 1 audit snapshot, got %d", n, scans)
			}
		})
	}
}

// BenchmarkCardinalityDecision 给出随 N 增长判定耗时保持平坦的基准证据。
// 运行：go test -run=NONE -bench=BenchmarkCardinalityDecision -benchtime=100x ./ontology
func BenchmarkCardinalityDecision(b *testing.B) {
	for _, n := range []int{100, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			env := newConstrainedEnvTB(b, 1, nil, n+b.N+10)
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("p%06d", i)
				if err := env.store.CreateInstance(id); err != nil {
					b.Fatal(err)
				}
				if _, err := env.store.commitRaider("x", []Op{{TypeID: testLinkType, Side: SideA, Other: id, Add: true}}); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				other := fmt.Sprintf("z%06d", i)
				if err := env.store.CreateInstance(other); err != nil {
					b.Fatal(err)
				}
				// 直接测量纯判定路径（不含写）：单次 O(K) 计数器读。
				_, _, _, err := env.store.checkCardinality("x", []Op{
					{TypeID: testLinkType, Side: SideA, Other: other, Add: true},
				})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
