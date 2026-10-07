package retention_test

import "ontology/retention"

func Example() {
	clk := retention.NewLogicalClock(1000)
	log := retention.NewAuditLog(nil)
	svc := retention.NewService(clk, log)

	_ = svc.CreateObject("order-1", map[string]string{"amount": "99"})
	_ = svc.CreateObject("customer-7", nil)
	_ = svc.AddEdge("order-1", "customer-7")

	// 用户请求删除，给 7 天（这里以毫秒为单位的示例时刻）宽限。
	_ = svc.SoftDelete("order-1", 1000+7*24*3600*1000)

	// 宽限期内一般使用者看不到，数据管理员看得到。
	_, _ = svc.Query("order-1", retention.RoleUser)
	_, _ = svc.Query("order-1", retention.RoleAdmin)

	// 撤销，删除影响被抹去；或推进时钟越过截止时刻，
	// 下一次任何涉及该对象的操作自动归档。
	_ = svc.Undelete("order-1")
	// Output:
}
