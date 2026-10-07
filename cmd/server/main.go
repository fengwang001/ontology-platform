// 命令 server 演示链接基数下调的级联处理流程：
// 确定性超额标记 -> 待处理 -> 保留/删除/恢复 -> 审计轨迹。
package main

import (
	"fmt"
	"log"

	"ontology/ontology"
)

func main() {
	m := ontology.NewManager()
	if err := m.DefineLinkType("works-for", "Employee", "Company", 4, ontology.Unlimited); err != nil {
		log.Fatal(err)
	}
	m.RegisterObject("alice")
	for i := 0; i < 4; i++ {
		m.RegisterObject(fmt.Sprintf("c%d", i))
		if err := m.CreateLink("works-for", fmt.Sprintf("l%d", i), "alice", fmt.Sprintf("c%d", i)); err != nil {
			log.Fatal(err)
		}
	}
	derived, err := m.RegisterDerived("l3", "headcount-agg")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("== 下调上限 4 -> 2 ==")
	if err := m.SetLimit("works-for", ontology.DirectionOut, 2); err != nil {
		log.Fatal(err)
	}
	printLinks(m)
	v, _ := m.QueryDerived(derived)
	fmt.Printf("派生状态 %s: stale=%v reason=%q\n", derived, v.Stale, v.StaleReason)

	fmt.Println("\n== 显式保留 l3（有效上限相应提升） ==")
	if err := m.ResolvePending("l3", ontology.ResolveKeep); err != nil {
		log.Fatal(err)
	}
	printLinks(m)

	fmt.Println("\n== 默认清理 l2（孤儿清理，仍超额 -> 删除） ==")
	if err := m.FinalizePending("l2"); err != nil {
		log.Fatal(err)
	}
	printLinks(m)
	if _, err := m.QueryDerived(derived); err != nil {
		fmt.Printf("派生状态 %s 已随链接保留而恢复可信；查询 err=%v\n", derived, err)
	}

	fmt.Println("\n== 审计轨迹 ==")
	for _, ev := range m.AuditLog() {
		fmt.Printf("#%d %-22s link=%-3s dir=%-3s trigger=%-28s disposition=%s\n",
			ev.Seq, ev.Kind, ev.LinkID, ev.Direction, ev.Trigger, ev.Disposition)
	}
}

func printLinks(m *ontology.Manager) {
	for _, v := range m.QueryLinks("works-for", ontology.DirectionOut, "alice") {
		fmt.Printf("  %s -> %s  [%s]\n", v.ID, v.Target, v.Status)
	}
	stats := m.Stats("works-for", ontology.DirectionOut, "alice")
	fmt.Printf("  active=%d pending=%d total=%d effectiveLimit=%d\n",
		stats.Active, stats.Pending, stats.TotalRegistered, stats.EffectiveLimit)
}
