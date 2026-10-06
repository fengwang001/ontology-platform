// Package deposit 实现退房后租赁押金的退还、扣减申报、受偿次序、
// 租户争议冻结、裁定执行与法定退还时限违约责任。
//
// 典型用法：
//
//	cfg := deposit.Config{A: 5, B: 3, C: 4, RateNum: 1, RateDen: 100}
//	svc := deposit.NewService(cfg, log.Printf)
//	_ = svc.Checkout(0, "lease-1", 5000)
//	id, _ := svc.FileClaim(2, "lease-1", deposit.Damage, 800)
//	_ = svc.Dispute(7, "lease-1", id)
//	principal, liability, _ := svc.Refund(10, "lease-1")
//
// 时限（均为整数日，退房日为第 0 天）：
//   - 申报期 [0, A]：第 A 天当天可申报/撤销，次日拒绝；
//   - 争议期 [A+1, A+B]：只能在此区间对扣项争议，每条仅一次；
//   - 退还期 [起算日, 起算日+C]：第 C 天当天退还免责，次日起计违约金。
//
// 受偿次序固定为 欠租 > 损坏 > 清洁 > 其他，同类别按申报先后；
// 不足额时后续扣项部分受偿，未受偿部分构成房东对租户的应收。
//
// 所有方法可并发调用；被拒绝的操作不改变任何状态与时钟。
// 设计取舍见同目录 DESIGN.md，正确性由边界测试与朴素模型随机差分测试保证。
package deposit
