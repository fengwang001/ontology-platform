// 演示：跨链接聚合视图的增量维护、默认时区版本迁移与滞后事件处理。
package main

import (
	"fmt"
	"time"

	"ontology/ontology"
)

func main() {
	p := ontology.NewPlatform()
	p.AddObjectType("Order", "createdAt", &ontology.TzDefVersion{Version: 1, ZoneID: "UTC", EffectiveSeq: 1})
	p.AddObjectType("Shipment", "shippedAt", &ontology.TzDefVersion{Version: 1, ZoneID: "UTC+08:00", EffectiveSeq: 1})
	p.AddLink("order-shipment", "Order", "Shipment")
	p.CreateView("orders-by-day", "order-shipment")
	local := func(d, h int) time.Time {
		return time.Date(2024, 3, d, h, 0, 0, 0, time.UTC)
	}
	// 第一批写入并维护。
	p.WriteTimeProperty("Order", "o1", local(5, 23))
	p.WriteTimeProperty("Shipment", "s1", local(6, 7)) // @UTC+8 → 与 o1 同一 UTC 日桶
	res := p.Maintain("orders-by-day")
	fmt.Printf("maintain #1: applied=%d topError=%v\n", res.Applied, res.TopError)
	// 默认时区定义版本迁移：Order 改用 UTC+08:00，自写入序号 4 起生效。
	err := p.MigrateDefaultTz("Order", ontology.TzDefVersion{Version: 2, ZoneID: "UTC+08:00", EffectiveSeq: 4})
	fmt.Printf("migrate Order tz v2: err=%v\n", err)
	// 滞后到达的事件（写入序号 3，迁移生效前写入，锚定 v1）。
	p.InjectEvent(ontology.ChangeEvent{
		ObjectID: "o-late", ObjectTypeID: "Order", WriteSeq: 3,
		LocalValue: local(5, 23), AnchoredTzVersion: 1, AnchoredZoneID: "UTC",
	})
	// 迁移生效后的新写入（写入序号 4，锚定 v2）。
	p.WriteTimeProperty("Order", "o2", local(6, 12))
	res = p.Maintain("orders-by-day")
	fmt.Printf("maintain #2: applied=%d stale=%d topError=%v\n", res.Applied, res.Stale, res.TopError)
	groups, stats := p.Query("orders-by-day")
	fmt.Printf("\nview (visited groups=%d entries=%d):\n", stats.GroupsVisited, stats.EntriesVisited)
	for _, g := range groups {
		fmt.Printf("  group day-bucket %d:\n", g.Key)
		for _, m := range g.Members {
			fmt.Printf("    %-8s %-8s utc=%s\n", m.ObjectTypeID, m.ObjectID,
				time.Unix(m.InstantUnix, 0).UTC().Format(time.RFC3339))
		}
	}
	fmt.Println("\ndecision log:")
	for _, d := range p.DecisionLog("orders-by-day") {
		fmt.Printf("  #%d %-8s obj=%-6s tzVersion=%d zone=%-9s group=%d %s\n",
			d.Seq, d.Op, d.ObjectID, d.AnchoredTzVersion, d.ZoneID, d.GroupKey, d.Note)
	}
}
