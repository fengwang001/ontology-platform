// atpdemo 演示多仓可承诺量查询与订单承诺系统的典型流程。
package main

import (
	"fmt"

	"ontology/atp/clock"
	"ontology/atp/order"
	"ontology/atp/service"
)

func main() {
	s := service.New()
	check := func(err error) {
		if err != nil {
			fmt.Println("  拒绝:", err)
		}
	}

	fmt.Println("== 建仓与补货 ==")
	check(s.AddWarehouse(0, "华东仓", 1))
	check(s.AddWarehouse(0, "华北仓", 2))
	check(s.AddStock(0, "华东仓", "手机", 5))
	check(s.AddStock(0, "华北仓", "手机", 8))
	check(s.ScheduleInbound(0, "华东仓", "手机", "po-1", 100, 20))

	atp := func(wh, sku string, at int64) {
		v, err := s.QueryATP(wh, sku, clock.Time(at))
		fmt.Printf("  ATP(%s,%s,@%d) = %v (err=%v)\n", wh, sku, at, v, err)
	}
	atp("华东仓", "手机", 0)
	atp("华东仓", "手机", 100)

	fmt.Println("== 订单承诺（拆分，上限 2 仓） ==")
	res, err := s.Commit(order.Request{
		OrderID: "SO-1",
		Lines: []order.Line{
			{SKU: "手机", Qty: 10},
		},
		Now:           clock.Time(10),
		AllowSplit:    true,
		TTL:           3600,
		MaxWarehouses: 2,
	})
	if err != nil {
		fmt.Println("  拒绝:", err)
	} else {
		fmt.Printf("  成功，到期时刻 %d，分配：%v\n", res.Expiry, res.Allocations)
	}

	fmt.Println("== 确认出库 ==")
	check(s.ConfirmOutbound(20, "SO-1"))
	atp("华东仓", "手机", 20)
	atp("华北仓", "手机", 20)

	fmt.Println("== 缺货分类 ==")
	_, err = s.Commit(order.Request{
		OrderID: "SO-2", Lines: []order.Line{{SKU: "手机", Qty: 100}},
		Now: clock.Time(30), AllowSplit: true, TTL: 60, MaxWarehouses: 2,
	})
	fmt.Println("  超量承诺:", err)
}
