package ontology

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// serialReplay 测试内的微型顺序回放器：按事实日志的全局序号顺序
// 逐条重放，作为"串行执行"的参照结果。
func serialReplay(def LinkTypeDef, facts []Fact, rt, vt int64) map[pair]bool {
	states := map[pair]*pairState{}
	for _, f := range facts {
		if f.RecordedAt > rt {
			continue
		}
		applyFact(states, f, def)
	}
	out := map[pair]bool{}
	for p, ps := range states {
		for _, iv := range ps.ivs {
			if iv.covers(vt) {
				out[p] = true
				break
			}
		}
	}
	return out
}

// TestConcurrentOpsLinearizable 并发发起创建、撤销、约束版本调整与审计，
// 最终可观察状态必须等价于按全局序号串行执行的结果。
func TestConcurrentOpsLinearizable(t *testing.T) {
	s := newTestStore(t)
	const lt = LinkTypeID("worksAt")

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 200; i++ {
				a := ObjectID(fmt.Sprintf("p%d", rng.Intn(10)))
				b := ObjectID(fmt.Sprintf("c%d", rng.Intn(10)))
				switch rng.Intn(6) {
				case 0, 1, 2:
					_ = s.CreateLink(lt, a, b, int64(rng.Intn(20)))
				case 3:
					_ = s.RevokeLink(lt, a, b, int64(rng.Intn(20)))
				case 4:
					_, _ = s.AdjustCardinality(lt, Cardinality{Max: rng.Intn(3)}, unconstrained)
				default:
					_, _ = s.Audit(AuditRequest{
						LinkType: lt, RecordFrom: 3, RecordTo: s.Now(),
						ValidAt: 10, ExpectedVersion: -1, // 必然作废：只验证错误路径同样线性化
					})
				}
			}
		}(int64(g)*977 + 13)
	}
	wg.Wait()

	// 等价性：并发后的最终回放 == 按 seq 顺序串行重放事实日志。
	s.mu.Lock()
	st := s.links[lt]
	facts := make([]Fact, len(st.facts))
	copy(facts, st.facts)
	def := st.def
	now := s.seq
	s.mu.Unlock()

	for _, vt := range []int64{0, 7, 19} {
		want := serialReplay(def, facts, now, vt)
		got := s.Replay(lt, now, vt)
		if len(got.Links) != len(want) {
			t.Fatalf("vt=%d: concurrent result %d links != serial %d links", vt, len(got.Links), len(want))
		}
		for _, l := range got.Links {
			if !want[pair{L: l.Left, R: l.Right}] {
				t.Fatalf("vt=%d: link (%s,%s) not in serial replay", vt, l.Left, l.Right)
			}
		}
		// 并发压力下镜像一致性仍成立。
		assertMirror(t, s, lt, now, vt)
	}
}
