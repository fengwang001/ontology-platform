// 命令 provenance-demo 演示双时态溯源查询的关键场景：
//   - 链接区间在边界时刻被追溯修正，旧查询结果保持稳定、新查询结果改变
//   - 「此刻未建立」与「已建立但尚不可见」两类不可见原因
//   - 多跳遍历在中间一跳因三方条件不满足而被截断
package main

import (
	"fmt"
	"time"

	"ontology/bitemporal"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	t := func(h int) time.Time { return time.Unix(int64(h)*3600, 0).UTC() }
	iv := func(a, b int) bitemporal.Interval {
		return bitemporal.Interval{From: t(a), To: t(b)}
	}

	store := bitemporal.NewStore()
	logger := bitemporal.NewMemoryLogger()
	engine := bitemporal.NewEngine(store, logger)

	// 对象 A --L--> B，A 再经 M 到 C。
	must(store.AppendObject(bitemporal.ObjectRecord{ID: "A", VersionID: "A1", Valid: iv(0, 100), WrittenAt: t(10)}))
	must(store.AppendObject(bitemporal.ObjectRecord{ID: "B", VersionID: "B1", Valid: iv(0, 100), WrittenAt: t(10)}))
	must(store.AppendObject(bitemporal.ObjectRecord{ID: "C", VersionID: "C1", Valid: iv(0, 100), WrittenAt: t(10)}))

	// L 初始区间 [20,40)，写入于 15；之后在写入时间 50 被修正为 [20,60)。
	must(store.AppendLink(bitemporal.LinkRecord{ID: "L", VersionID: "L1", SourceID: "A", TargetID: "B", Valid: iv(20, 40), WrittenAt: t(15)}))
	must(store.AppendLink(bitemporal.LinkRecord{ID: "M", VersionID: "M1", SourceID: "B", TargetID: "C", Valid: iv(0, 30), WrittenAt: t(12)}))

	q := bitemporal.Query{SourceID: "A", ValidAt: t(50), AsOf: t(30), MaxDepth: 2}
	r, err := engine.AsOf(q)
	must(err)
	fmt.Printf("asOf=30, validAt=50: paths=%d blocked=%d (修正尚未落定，L 不可见)\n", len(r.Paths), len(r.Blocked))

	must(store.AppendLink(bitemporal.LinkRecord{ID: "L", VersionID: "L2", SourceID: "A", TargetID: "B", Valid: iv(20, 60), WrittenAt: t(50)}))

	r, err = engine.AsOf(q)
	must(err)
	fmt.Printf("asOf=30, validAt=50: paths=%d blocked=%d (历史查询结果不因修正而改变)\n", len(r.Paths), len(r.Blocked))

	q2 := bitemporal.Query{SourceID: "A", ValidAt: t(50), AsOf: t(60), MaxDepth: 2}
	r2, err := engine.AsOf(q2)
	must(err)
	fmt.Printf("asOf=60, validAt=50: paths=%d\n", len(r2.Paths))
	for _, p := range r2.Paths {
		fmt.Printf("  path: %v via %v evidence-link-versions=%v\n", p.Nodes, p.Links, linkVersions(p.Evidence))
	}
	for _, b := range r2.Blocked {
		fmt.Printf("  blocked: %s -> %s via %s combined=%d (1=未建立 2=尚不可见)\n",
			b.Prefix[len(b.Prefix)-1], b.TargetID, b.LinkID, b.Combined)
	}

	fmt.Printf("logged queries: %d\n", len(logger.Entries()))
}

func linkVersions(ev []bitemporal.HopEvidence) []string {
	out := make([]string, len(ev))
	for i, e := range ev {
		out[i] = e.LinkVersionID
	}
	return out
}
