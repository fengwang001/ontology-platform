package ontology

import (
	"fmt"
	"testing"
)

// 判定开销与关系网络总规模无关（可观测证明）：
// 在保持目标实例相关的标签来源与角色层级不变的前提下，
// 将对象类型/链接类型/标签/授权的总规模放大两个数量级，
// 判定遍历的标签来源数与角色节点数必须保持逐值相等。
func TestDecisionCostIndependentOfGraphSize(t *testing.T) {
	build := func(extra int) *Engine {
		e := New(nil)
		must(t, e.AddObjectType("O"))
		must(t, e.AddTag("T"))
		must(t, e.AttachTag(Attachment{ObjectType: "O", Tag: "T"}))
		must(t, e.AddRole("r0"))
		must(t, e.AddRole("r1", "r0"))
		must(t, e.AddRole("r2", "r1"))
		must(t, e.AddGrant(Grant{Role: "r0", Tag: "T", Effect: Allow}))
		must(t, e.AddSubject("s", "r2"))
		must(t, e.AddInstance("i", "O"))
		must(t, e.AddLinkType("L"))
		must(t, e.AddPropagation(Propagation{Tag: "T", LinkType: "L", Direction: Downstream}))
		prev := ""
		for k := 0; k < extra; k++ {
			ot := fmt.Sprintf("X%d", k)
			tag := fmt.Sprintf("TX%d", k)
			role := fmt.Sprintf("RX%d", k)
			must(t, e.AddObjectType(ot))
			must(t, e.AddTag(tag))
			must(t, e.AttachTag(Attachment{ObjectType: ot, Tag: tag}))
			must(t, e.AddRole(role))
			must(t, e.AddGrant(Grant{Role: role, Tag: tag, Effect: Deny}))
			if prev != "" {
				must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: prev, To: ot}))
			}
			prev = ot
		}
		if extra > 0 {
			must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: prev, To: "X0"}))
		}
		return e
	}
	base := build(0).Decide("s", "i")
	if !base.Allowed {
		t.Fatalf("基准判定应允许: %+v", base)
	}
	for _, extra := range []int{10, 100, 1000} {
		d := build(extra).Decide("s", "i")
		if !d.Allowed {
			t.Fatalf("extra=%d 时判定结果改变: %+v", extra, d)
		}
		if d.Stats != base.Stats {
			t.Fatalf("extra=%d 时判定开销随规模增长: %+v vs %+v", extra, d.Stats, base.Stats)
		}
	}
}
