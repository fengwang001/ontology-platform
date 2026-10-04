package issue

import (
	"math/rand"
	"testing"

	"ontology/bloodstock"
)

func TestLandPopBoundAcrossRandomOps(t *testing.T) {
	for g := 0; g < 300; g++ {
		rng := rand.New(rand.NewSource(int64(g + 9000)))
		M := int64(rng.Intn(200))
		H := int64(rng.Intn(800))
		ops := genOps(rng, int64(g))
		m := New(M, H)
		m.Grant("tech", RoleTech)
		m.Grant("issue1", RoleIssue)
		m.Grant("issue2", RoleIssue)
		m.Grant("sup", RoleSupervisor)
		for _, o := range ops {
			runReal(m, o)
		}
		// 触发一次覆盖全部到期的落地：直接读取底层累计计数器。
		if m.EventsPopped() > m.EventsApplied()+1 {
			t.Fatalf("g=%d 事件取出 %d > 落地 %d+1", g, m.EventsPopped(), m.EventsApplied())
		}
	}
}

func TestCrossmatchExaminedBoundRandom(t *testing.T) {
	// 逐次 Crossmatch 核对 examined <= n + 当次效期排除数 + 8。
	for g := 0; g < 300; g++ {
		rng := rand.New(rand.NewSource(int64(g + 30000)))
		M := int64(rng.Intn(300))
		H := int64(rng.Intn(900))
		m := New(M, H)
		m.Grant("tech", RoleTech)
		nav := newNaive(M, H)
		nav.grant("tech", RoleTech)
		ops := genOps(rng, int64(g))
		for _, o := range ops {
			excluded := 0
			if o.kind == opCross {
				nav.land(o.now)
				rank := map[int]bool{}
				for _, sl := range nav.order(o.args[1]) {
					rank[sl] = true
				}
				for _, b := range nav.bags {
					if b.status == bloodstock.Available && b.exp <= o.now+M && rank[bloodstock.Slot(b.abo, b.rh)] {
						excluded++
					}
				}
			}
			_, examined, ge := mCross(m, o)
			_, _ = nav.run(o)
			if ge == nil && o.kind == opCross && examined > o.n+excluded+8 {
				t.Fatalf("g=%d %+v examined=%d > n=%d+excl=%d+8", g, o, examined, o.n, excluded)
			}
		}
	}
}

func mCross(m *Manager, o op) ([]string, int, error) {
	if o.kind != opCross {
		_, e := runReal(m, o)
		return nil, 0, e
	}
	return m.Crossmatch(o.now, o.args[0], o.args[1], o.n)
}
