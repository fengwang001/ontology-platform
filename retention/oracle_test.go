package retention_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/retention"
)

// 在大量随机操作序列上，将生产实现与独立朴素模型逐条比对：
// 错误码、对象状态、两类身份可见性、出边可见性必须一致。
func TestDifferentialAgainstNaiveOracle(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			clk := retention.NewLogicalClock(0)
			svc := retention.NewService(clk, nil)
			oracle := &naiveOracle{}

			ids := []string{"a", "b", "c"}
			pairs := [][2]string{{"a", "b"}, {"b", "c"}, {"a", "c"}}

			step := func(ev nEvent) {
				t.Helper()
				ev.t = oracle.now
				var gotErr error
				switch ev.kind {
				case "advance":
					clk.Advance(ev.t)
					svc.Advance(ev.t)
					oracle.now = ev.t
				case "create":
					gotErr = svc.CreateObject(ev.id, map[string]string{"v": "1"})
				case "edge":
					gotErr = svc.AddEdge(ev.id, ev.target)
				case "delete":
					gotErr = svc.SoftDelete(ev.id, ev.param)
				case "undelete":
					gotErr = svc.Undelete(ev.id)
				case "freeze":
					gotErr = svc.Freeze(ev.id, ev.param)
				case "archive":
					gotErr = svc.Archive(ev.id)
				}
				if ev.kind != "advance" && ev.kind != "query" && ev.kind != "edgevis" {
					want := oracle.expectErr(ev)
					got := int(retention.Code(gotErr))
					if got != want {
						t.Fatalf("seed=%d event=%+v want err %d got %d", seed, ev, want, got)
					}
				}
				if ev.kind != "advance" {
					oracle.events = append(oracle.events, ev)
				}

				// 每次操作后逐对象、逐身份比对状态与可见性。
				for _, id := range ids {
					for _, role := range []retention.Role{retention.RoleUser, retention.RoleAdmin} {
						v, exists := svc.Query(id, role)
						// 记录查询事件（查询也触发宽限到期）
						oracle.events = append(oracle.events, nEvent{
							t: oracle.now, kind: "query", id: id, role: int(role),
						})
						ex, vis, st, gd, fd := oracle.visible(id, role == retention.RoleAdmin, len(oracle.events))
						if exists != ex {
							t.Fatalf("seed=%d exists mismatch id=%s: %v vs %v", seed, id, exists, ex)
						}
						if ex {
							if v.Visible != vis {
								t.Fatalf("seed=%d visible mismatch id=%s role=%v: %v vs %v",
									seed, id, role, v.Visible, vis)
							}
							if vis && mapState(v.State) != st {
								t.Fatalf("seed=%d state mismatch id=%s: %v vs %v", seed, id, v.State, st)
							}
							if role == retention.RoleAdmin && v.Visible {
								if v.GraceDeadline != gd || v.FreezeDeadline != fd {
									t.Fatalf("seed=%d deadline mismatch id=%s: (%d,%d) vs (%d,%d)",
										seed, id, v.GraceDeadline, v.FreezeDeadline, gd, fd)
								}
								if st == nFrozen && v.Attributes != nil {
									t.Fatalf("seed=%d frozen attrs leaked", seed)
								}
							}
						}
					}
					// 出边可见性跟随源对象。
					for _, p := range pairs {
						if p[0] != id {
							continue
						}
						for _, role := range []retention.Role{retention.RoleUser, retention.RoleAdmin} {
							got := svc.EdgeVisible(p[0], p[1], role)
							oracle.events = append(oracle.events, nEvent{
								t: oracle.now, kind: "edgevis", id: p[0], target: p[1], role: int(role),
							})
							_, srcVis, srcSt, _, _ := oracle.visible(p[0], role == retention.RoleAdmin, len(oracle.events))
							// 冻结管理员视图不出边（业务关系遮蔽）；边是否登记过见下。
							edgeKnown := svc.EdgeKnown(p[0], p[1])
							want := edgeKnown && srcVis &&
								!(role == retention.RoleAdmin && srcSt == nFrozen)
							if got != want {
								t.Fatalf("seed=%d edge mismatch %s->%s role=%v: got %v want %v (st=%v)",
									seed, p[0], p[1], role, got, want, srcSt)
							}
						}
					}
				}
			}

			// 先建对象与边
			for _, id := range ids {
				step(nEvent{kind: "create", id: id})
			}
			for _, p := range pairs {
				step(nEvent{kind: "edge", id: p[0], target: p[1]})
			}

			for i := 0; i < 300; i++ {
				id := ids[rng.Intn(len(ids))]
				switch rng.Intn(7) {
				case 0:
					step(nEvent{kind: "advance", t: oracle.now + int64(rng.Intn(20))})
				case 1:
					step(nEvent{kind: "delete", id: id, param: oracle.now + int64(1+rng.Intn(15))})
				case 2:
					step(nEvent{kind: "undelete", id: id})
				case 3:
					step(nEvent{kind: "freeze", id: id, param: int64(1 + rng.Intn(15))})
				case 4:
					step(nEvent{kind: "archive", id: id})
				case 5:
					step(nEvent{kind: "delete", id: id, param: oracle.now - int64(rng.Intn(5))}) // 非法参数
				case 6:
					step(nEvent{kind: "freeze", id: id, param: -int64(rng.Intn(3))}) // 非法参数
				}
			}
		})
	}
}

func mapState(s retention.State) nState {
	switch s {
	case retention.StateAlive:
		return nAlive
	case retention.StateGrace:
		return nGrace
	case retention.StateFrozen:
		return nFrozen
	case retention.StateArchived:
		return nArchived
	default:
		return nMissing
	}
}
