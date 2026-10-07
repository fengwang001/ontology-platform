package ontology

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// 并发标签声明变更、链接关系变更与访问判定：
// 竞态检测器下不应有数据竞争；并发阶段结束后，
// 引擎状态必须与按同一串行顺序重放的结果一致。
func TestConcurrentMixedOperations(t *testing.T) {
	e := New(nil)
	for i := 0; i < 8; i++ {
		must(t, e.AddObjectType(fmt.Sprintf("OT%d", i)))
	}
	must(t, e.AddLinkType("L"))
	for i := 0; i < 4; i++ {
		must(t, e.AddTag(fmt.Sprintf("T%d", i)))
	}
	must(t, e.AddRole("R0"))
	must(t, e.AddSubject("S0", "R0"))
	for i := 0; i < 8; i++ {
		must(t, e.AddInstance(fmt.Sprintf("I%d", i), fmt.Sprintf("OT%d", i)))
	}

	var writers, readers sync.WaitGroup
	// 写协程：交替执行链接、挂载、传播、阻断与授权变更。
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(seed int64) {
			defer writers.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 200; i++ {
				tag := fmt.Sprintf("T%d", r.Intn(4))
				ot := fmt.Sprintf("OT%d", r.Intn(8))
				edge := LinkEdge{LinkType: "L", From: ot, To: fmt.Sprintf("OT%d", r.Intn(8))}
				switch r.Intn(6) {
				case 0:
					e.AddLinkEdge(edge)
				case 1:
					e.RemoveLinkEdge(edge)
				case 2:
					e.AttachTag(Attachment{ObjectType: ot, Tag: tag})
				case 3:
					e.DetachTag(Attachment{ObjectType: ot, Tag: tag})
				case 4:
					p := Propagation{Tag: tag, LinkType: "L", Direction: Direction(r.Intn(2))}
					if r.Intn(2) == 0 {
						e.AddPropagation(p)
					} else {
						e.RemovePropagation(p)
					}
				case 5:
					g := Grant{Role: "R0", Tag: tag, Effect: Effect(r.Intn(2))}
					if r.Intn(2) == 0 {
						e.AddGrant(g)
					} else {
						e.RemoveGrant(g)
					}
				}
			}
		}(int64(w))
	}
	// 读协程：持续判定，结果必须是某个串行顺序下的合法结论。
	validReasons := map[ReasonCode]bool{
		ReasonAllowed: true, ReasonNoGrant: true, ReasonExplicitDeny: true,
		ReasonDenyOverrides: true, ReasonRoleCycle: true,
		ReasonAllPathsBlocked: true, ReasonNotFound: true, ReasonTagNotPresent: true,
	}
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		readers.Add(1)
		go func(seed int64) {
			defer readers.Done()
			r := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				d := e.Decide("S0", fmt.Sprintf("I%d", r.Intn(8)))
				if !validReasons[d.Reason] {
					t.Errorf("非法判定原因: %s", d.Reason)
					return
				}
				e.DecideTag("S0", fmt.Sprintf("I%d", r.Intn(8)), fmt.Sprintf("T%d", r.Intn(4)))
			}
		}(int64(100 + w))
	}
	writers.Wait()
	close(stop)
	readers.Wait()
}

// 并发判定的线性一致性抽查：对同一实例并发发起相同判定，
// 在无任何并发变更时，全部结果必须逐字段一致。
func TestConcurrentDecisionsConsistent(t *testing.T) {
	e := New(nil)
	must(t, e.AddObjectType("O"))
	must(t, e.AddTag("T"))
	must(t, e.AttachTag(Attachment{ObjectType: "O", Tag: "T"}))
	must(t, e.AddRole("R"))
	must(t, e.AddSubject("S", "R"))
	must(t, e.AddInstance("I", "O"))
	must(t, e.AddGrant(Grant{Role: "R", Tag: "T", Effect: Allow}))

	want := e.Decide("S", "I")
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				got := e.Decide("S", "I")
				if got.Allowed != want.Allowed || got.Reason != want.Reason ||
					got.Stats != want.Stats {
					t.Errorf("并发判定结果不一致: %+v vs %+v", got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}
