// 演示：事件溯源重建 + 追溯孤儿判定的完整流程。
package main

import (
	"fmt"

	"ontology/orphan"
)

func main() {
	// v1：对象类型 Server 不强制任何必需链接。
	s := orphan.NewStore(orphan.RuleSpec{
		RequiredLinkTypes: map[orphan.ObjectTypeID][]orphan.LinkTypeID{},
		GracePeriod:       5,
	}, 0)

	must(s.Append(
		orphan.Event{ID: "lt1", Kind: orphan.EvLinkTypeCreated, LinkType: "hosts", Time: 0},
		orphan.Event{ID: "o1", Kind: orphan.EvObjectCreated, Object: "vm-1", ObjectType: "Server", Time: 10},
		orphan.Event{ID: "o2", Kind: orphan.EvObjectCreated, Object: "rack-1", ObjectType: "Rack", Time: 10},
		orphan.Event{ID: "l1", Kind: orphan.EvLinkCreated, Link: "lk-1", LinkType: "hosts", From: "rack-1", To: "vm-1", Time: 12},
		orphan.Event{ID: "r1", Kind: orphan.EvLinkRevoked, Link: "lk-1", Time: 20},
		// 事件流中没有记录任何级联清理；vm-1 之后仍在被正常使用。
		orphan.Event{ID: "ps1", Kind: orphan.EvPropertySet, Object: "rack-1", Key: "location", Value: "cn-north", Time: 27},
	))

	// v2：追溯适用——Rack 的 hosts 链接变为必需（宽限 5）。
	v2, err := s.AdjustRule(orphan.RuleSpec{
		RequiredLinkTypes: map[orphan.ObjectTypeID][]orphan.LinkTypeID{"Rack": {"hosts"}},
		GracePeriod:       5,
	}, true, 30)
	must2(err)

	for _, at := range []orphan.Time{24, 25, 40} {
		det, err := s.Determine(orphan.Query{Object: "rack-1", At: at, Version: v2})
		if err != nil {
			fmt.Printf("t=%d 判定失败: %v\n", at, err)
			continue
		}
		fmt.Printf("t=%d 状态=%v zeroSince=%d orphanDue=%d 后续活动=%d 检视事件=%d\n",
			at, det.Status, det.ZeroSince, det.OrphanDue, len(det.Activities), det.EventsScanned)
	}

	// 用已作废的 v1 判定历史时刻 → RuleVersionVoided。
	if _, err := s.Determine(orphan.Query{Object: "rack-1", At: 25, Version: 1}); err != nil {
		fmt.Printf("v1 已作废: %v\n", err)
	}

	st, err := s.RebuildNetwork(40)
	must2(err)
	fmt.Printf("t=40 网络: 对象=%d 链接=%d rack-1 孤儿标记=%v\n",
		len(st.Objects), len(st.Links), st.Objects["rack-1"].MarkedOrphan)

	fmt.Printf("审计记录 %d 条:\n", len(s.AuditLog()))
	for _, rec := range s.AuditLog() {
		fmt.Printf("  seq=%d 对象=%s t=%d 声明=v%d 治理=v%d 状态=%v 错误=%v\n",
			rec.Seq, rec.Query.Object, rec.Query.At, rec.Declared, rec.Governing, rec.Status, rec.Err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func must2(err error) {
	if err != nil {
		panic(err)
	}
}
