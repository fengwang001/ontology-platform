// demo 演示权限分级抢占：低权限动作在重试循环中被高权限写入立即终止。
package main

import (
	"fmt"
	"sync"

	"ontology/ontology"
)

func main() {
	store := ontology.NewStore()
	store.Create("obj-1", map[string]any{"count": 0})

	parked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	exec := ontology.NewExecutor(store, ontology.Hooks{
		BeforeCommit: func(a ontology.Action, baseline int64) {
			if a.ID == "low" {
				once.Do(func() { close(parked); <-release })
			}
		},
	})

	inc := func(props map[string]any) ontology.Mutation {
		n, _ := props["count"].(int)
		return ontology.Mutation{Set: map[string]any{"count": n + 1}}
	}

	lowRes := make(chan ontology.Result, 1)
	go func() {
		lowRes <- exec.Run(ontology.Action{ID: "low", ObjectID: "obj-1", Priority: 1, MaxRetries: 10, Apply: inc})
	}()

	<-parked // low 已持有基线 0，停在提交前
	high := ontology.NewExecutor(store, ontology.Hooks{})
	res := high.Run(ontology.Action{ID: "high", ObjectID: "obj-1", Priority: 9, MaxRetries: 0, Apply: inc})
	fmt.Printf("high (p=9): final=%v version=%d\n", res.Final, res.Version)
	close(release)

	res = <-lowRes
	fmt.Printf("low  (p=1): final=%v attempts=%v\n", res.Final, res.Attempts)

	version, clock, props := store.State("obj-1")
	fmt.Printf("final state: version=%d clock=%d props=%v\n", version, clock, props)
	for _, ev := range store.Events() {
		fmt.Printf("event seq=%d action=%s priority=%d baseline=%d kind=%d version=%d\n",
			ev.Seq, ev.ActionID, ev.Priority, ev.Baseline, ev.Kind, ev.Version)
	}
}
