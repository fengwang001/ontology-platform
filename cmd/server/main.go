// Command server 演示对象实例双时态存储子系统的基本读写流程。
package main

import (
	"fmt"

	"ontology/core"
	"ontology/service"
)

func main() {
	e := service.NewEngine()
	k := core.Key{Type: "Customer", ID: "C-001"}

	must := func(v core.Version, err *core.Error) core.Version {
		if err != nil {
			fmt.Println("写入被拒:", err)
			return core.Version{}
		}
		fmt.Println("写入成功:", v)
		return v
	}

	// 正常写入 -> 逻辑删除 -> 复活
	must(e.Put(k, 100, `{"name":"Acme","tier":"gold"}`, "v0"))
	must(e.Remove(k, 200, "v1"))
	must(e.Put(k, 300, `{"name":"Acme","tier":"platinum"}`, "v2"))

	// 凭证过期的并发写入会被拒绝
	must(e.Put(k, 400, `{"name":"Acme","tier":"silver"}`, "v2"))

	fmt.Println()
	for _, bizQ := range []int64{50, 150, 250, 350} {
		fmt.Printf("Query(sys=latest, biz=%d) -> %s\n", bizQ, e.Query(k, e.LatestSeq(k), bizQ))
	}
	fmt.Printf("Query(sys=1, biz=150)   -> %s\n", e.Query(k, 1, 150))
	fmt.Printf("Query(never-written)    -> %s\n",
		e.Query(core.Key{Type: "Customer", ID: "ghost"}, 99, 100))

	fmt.Println("\n当前可见时间线:")
	for _, iv := range e.HistoryAsOf(k, e.LatestSeq(k)) {
		end := fmt.Sprint(iv.End)
		if iv.Open {
			end = "+inf"
		}
		fmt.Printf("  [%d, %s) -> %s\n", iv.Start, end, iv.Version)
	}
}
