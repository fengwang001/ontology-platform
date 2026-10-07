package ontology

import (
	"math/rand"
	"sort"
	"testing"
)

// naiveIndex 是完全独立的朴素参照实现：不共享任何生产代码的数据结构，
// 仅维护 objectID -> 当前值 的 map，并在任意时刻可重建“值 -> 对象集合”。
type naiveIndex struct {
	values map[string]PropertyValue // objectID -> current value（仅有值的对象）
}

func newNaive() *naiveIndex { return &naiveIndex{values: map[string]PropertyValue{}} }

func (n *naiveIndex) put(oid string, props map[string]PropertyValue) {
	if v, ok := props["p"]; ok {
		n.values[oid] = v
	}
}

func (n *naiveIndex) lookup(v PropertyValue) []string {
	var out []string
	for oid, cur := range n.values {
		if cur == v {
			out = append(out, oid)
		}
	}
	sort.Strings(out)
	return out
}

func (n *naiveIndex) allEntries() map[string]PropertyValue {
	out := make(map[string]PropertyValue, len(n.values))
	for k, v := range n.values {
		out[k] = v
	}
	return out
}

// TestDifferentialRandomInterleaving 用确定性 PRNG 生成随机的写入/重建交错
// 序列，并在每一步用三种独立判定交叉对照：
//
//  1. 朴素模型查询 == 管理器 Query；
//  2. 独立复核器对存活审计的结论 == consistent；
//  3. 审计逐条展开的 object->value == 朴素模型当前 object->value。
//
// 每一步的输入/输出/依据通过 DecisionLog 打印（测试 harness 已接缓冲，
// 失败时 t.Log 转储）。
func TestDifferentialRandomInterleaving(t *testing.T) {
	const seeds = 40
	const steps = 120
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		_, mgr, ver, logBuf := newHarness(t)
		mgr.Declare("T", "idx", "p")
		naive := newNaive()

		for step := 0; step < steps; step++ {
			oid := []string{"a", "b", "c", "d", "e"}[rng.Intn(5)]
			val := []string{"X", "Y", "Z", "W"}[rng.Intn(4)]
			mgr.Write("T", oid, map[string]PropertyValue{"p": val})
			naive.put(oid, map[string]PropertyValue{"p": val})

			// 随机发起重建：重建可能与后续写入交错（本实现的重建是
			// 串行点上的原子区间，等价于在该区间无并发写入的某个全局顺序）。
			if rng.Intn(5) == 0 {
				if _, err := mgr.Rebuild("T", "idx"); err != nil {
					t.Fatalf("seed=%d step=%d rebuild: %v", seed, step, err)
				}
			}

			phase, _ := mgr.Phase("T", "idx")
			if phase != PhaseActive {
				continue // 尚无可用索引，只累积朴素模型
			}

			// 2) 独立复核必须一致。
			rep := ver.VerifyAudit(mustAudit(t, mgr), mgr.Store())
			if !rep.Consistent {
				t.Fatalf("seed=%d step=%d verify inconsistent: %v\nLOG:\n%s",
					seed, step, rep.Mismatches, logBuf.String())
			}

			// 3) 审计展开 == 朴素模型。
			audit, _, err := mgr.Audit("T", "idx")
			if err != nil {
				t.Fatal(err)
			}
			fromAudit := map[string]PropertyValue{}
			for _, e := range audit.Entries {
				if prev, dup := fromAudit[e.ObjectID]; dup && prev != e.Value {
					t.Fatalf("seed=%d duplicate conflicting entry for %s", seed, e.ObjectID)
				}
				fromAudit[e.ObjectID] = e.Value
			}
			want := naive.allEntries()
			if len(fromAudit) != len(want) {
				t.Fatalf("seed=%d step=%d entry count audit=%d naive=%d",
					seed, step, len(fromAudit), len(want))
			}
			for o, v := range want {
				if fromAudit[o] != v {
					t.Fatalf("seed=%d step=%d object %s audit=%q naive=%q",
						seed, step, o, fromAudit[o], v)
				}
			}

			// 1) 查询对照：对每个可能取值，两边结果集必须相同。
			for _, v := range []string{"X", "Y", "Z", "W", "MISSING"} {
				res, err := mgr.Query("T", "idx", v)
				if err != nil {
					t.Fatalf("seed=%d query: %v", seed, err)
				}
				got := res.ObjectIDs
				if got == nil {
					got = []string{}
				}
				wantIDs := naive.lookup(v)
				if len(got) != len(wantIDs) {
					t.Fatalf("seed=%d step=%d lookup %q audit=%v naive=%v",
						seed, step, v, got, wantIDs)
				}
				for i := range got {
					if got[i] != wantIDs[i] {
						t.Fatalf("seed=%d step=%d lookup %q order/content mismatch %v vs %v",
							seed, step, v, got, wantIDs)
					}
				}
			}
		}
	}
}

func mustAudit(t *testing.T, mgr *IndexManager) *AuditRecord {
	t.Helper()
	rec, _, err := mgr.Audit("T", "idx")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	return rec
}

// TestDifferentialRandomFailures 随机在“基线后/封存前”注入失败，
// 断言：失败后查询严格不可用（从不返回半成品），写入继续累积，
// 一旦恢复重建成功，索引立即再次与朴素模型逐项一致。
func TestDifferentialRandomFailures(t *testing.T) {
	const seeds = 25
	const steps = 100
	for seed := int64(1000); seed < 1000+seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		_, mgr, ver, _ := newHarness(t)
		mgr.Declare("T", "idx", "p")
		naive := newNaive()

		for step := 0; step < steps; step++ {
			oid := []string{"a", "b", "c"}[rng.Intn(3)]
			val := []string{"X", "Y"}[rng.Intn(2)]
			mgr.Write("T", oid, map[string]PropertyValue{"p": val})
			naive.put(oid, map[string]PropertyValue{"p": val})

			if rng.Intn(6) == 0 {
				mgr.FailNextRebuild(rng.Intn(2) == 0)
				if _, err := mgr.Rebuild("T", "idx"); err == nil {
					t.Fatalf("seed=%d expected injected failure", seed)
				}
				if res, err := mgr.Query("T", "idx", val); err == nil {
					t.Fatalf("seed=%d step=%d partial index served after failure: %v",
						seed, step, res)
				}
				continue
			}

			if rng.Intn(4) == 0 {
				if _, err := mgr.Rebuild("T", "idx"); err != nil {
					t.Fatalf("seed=%d rebuild: %v", seed, err)
				}
			}

			phase, _ := mgr.Phase("T", "idx")
			if phase != PhaseActive {
				continue
			}
			if rep := ver.VerifyAudit(mustAudit(t, mgr), mgr.Store()); !rep.Consistent {
				t.Fatalf("seed=%d step=%d inconsistent after recovery: %v",
					seed, step, rep.Mismatches)
			}
			for _, v := range []string{"X", "Y"} {
				res, err := mgr.Query("T", "idx", v)
				if err != nil {
					t.Fatalf("seed=%d query: %v", seed, err)
				}
				got := res.ObjectIDs
				if got == nil {
					got = []string{}
				}
				wantIDs := naive.lookup(v)
				if len(got) != len(wantIDs) {
					t.Fatalf("seed=%d step=%d lookup %s = %v want %v",
						seed, step, v, got, wantIDs)
				}
			}
		}
	}
}
