package ontology_test

import (
	"context"
	"fmt"

	"ontology/ontology"
)

// ExampleExecutor 演示真实并发下的四类互斥结果与抢占语义。
// 这里使用无钩子执行器（goroutine 真并发），结果集合包含
// committed / preempted / exhausted；抢占由高水位 O(1) 判定。
func ExampleExecutor() {
	exec := ontology.NewExecutor(nil)

	setX := func(id int, priv ontology.Privilege, delta int, budget int) ontology.Op {
		return ontology.Op{
			ID:          id,
			Instance:    "obj",
			Priv:        priv,
			MaxAttempts: budget,
			Apply: func(a ontology.Attrs) ontology.Attrs {
				out := ontology.Attrs{}
				for k, v := range a {
					out[k] = v
				}
				n, _ := out["x"].(int)
				out["x"] = n + delta
				return out
			},
		}
	}

	high := setX(1, 10, 1, 1)
	low := setX(2, 1, 2, 5)

	done := make(chan ontology.OpResult, 2)
	go func() { done <- exec.Run(context.Background(), high) }()
	// 低权限动作在高权限生效之后才完成 CAS：它读到的基线已被
	// 权限 10 推进，因此立即抢占，不消耗 5 次预算。
	go func() { done <- exec.Run(context.Background(), low) }()

	for range 2 {
		r := <-done
		fmt.Printf("op%d=%s attempts=%d\n", r.OpID, r.Status, len(r.Attempts))
	}
	// 输出顺序在真并发下不保证，故不使用 Output 断言；
	// 确定性版本见包内测试与 DESIGN.md。
}
