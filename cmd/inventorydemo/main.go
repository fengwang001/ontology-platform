// inventorydemo 演示多仓库存承诺系统的典型流程：
// 现货 + 计划入库 + 预留共同作用下的可承诺量查询、订单承诺、
// 拆分、缺货分类、确认出库与释放。
package main

import (
	"fmt"

	"ontology/inventory"
)

func main() {
	s := inventory.NewSystem()
	check(s.AddWarehouse("华东仓", 1))
	check(s.AddWarehouse("华南仓", 2))

	check(s.AddOnHand("华东仓", "手机", 5, 0))
	check(s.AddOnHand("华南仓", "手机", 3, 0))
	check(s.AddPlannedInbound("华东仓", "手机", "PO-1001", 10, 20, 0))

	atp, err := s.ATP("华东仓", "手机", 10)
	check(err)
	fmt.Printf("[t=0] 华东仓手机在承诺时刻10的可承诺量 = %d（现货5 + 计划入库20）\n", atp)

	// 不允许拆分：单一仓库全量满足，选优先序号最小且量足够的仓。
	res, err := s.CommitOrder("ORD-1", []inventory.OrderLine{{Product: "手机", Qty: 4}}, 1, false, 100, 2)
	check(err)
	fmt.Printf("[t=1] ORD-1 承诺成功，到期=%d，分配=%v\n", res.ExpireAt, res.Allocations)

	// 允许拆分：按优先序贪心跨仓取用。
	res, err = s.CommitOrder("ORD-2", []inventory.OrderLine{{Product: "手机", Qty: 3}}, 2, true, 100, 2)
	check(err)
	fmt.Printf("[t=2] ORD-2 承诺成功，到期=%d，分配=%v\n", res.ExpireAt, res.Allocations)

	// 永久缺货 vs 暂时缺货。
	_, err = s.CommitOrder("ORD-3", []inventory.OrderLine{{Product: "手机", Qty: 1000}}, 3, true, 100, 2)
	fmt.Printf("[t=3] ORD-3 被拒绝: %v\n", err)
	_, err = s.CommitOrder("ORD-4", []inventory.OrderLine{{Product: "手机", Qty: 24}}, 3, true, 100, 2)
	fmt.Printf("[t=3] ORD-4 被拒绝: %v（总供给28足够，但受预留与到货时刻限制）\n", err)

	// 确认出库与释放。
	check(s.ConfirmOutbound("ORD-1", 4))
	fmt.Println("[t=4] ORD-1 确认出库，现货扣减完成")
	check(s.ReleaseReservation("ORD-2", 5))
	fmt.Println("[t=5] ORD-2 预留已释放，占用量归还")

	atp, err = s.ATP("华东仓", "手机", 10)
	check(err)
	fmt.Printf("[t=5] 华东仓手机在承诺时刻10的可承诺量 = %d\n", atp)
	check(s.Validate())
	fmt.Println("不变量校验通过：现货非负，有效预留不超过可承诺上限")
}

func check(err *inventory.Error) {
	if err != nil {
		panic(err)
	}
}
