// Package settlement 实现证券交割日终批处理与交割失败处理系统。
//
// 核心能力：
//   - 券款对付（DvP）：券与现金在同一把锁内按一致快照同时变动；
//   - 部分交割：按允许标志决定零割或全额；
//   - 失败滚动：未交割量按编号升序在后续营业日继续判定；
//   - 罚金：按日独立、ceil(cash*bps/10000)，只记应付应收；
//   - 强制了结：累计失败营业日数达到 B 当日罚金后取消，卖方责任另计补偿；
//   - 可复现：同一操作序列重放得到完全相同的头寸、罚金与状态。
//
// 典型用法：
//
//	sys, _ := settlement.NewSystem(settlement.Config{
//	    BusinessDays: []int64{1, 2, 3}, MaxFailDays: 3, PenaltyBPS: 100,
//	})
//	_ = sys.AddAccount("buyer", nil, 1000)
//	_ = sys.AddAccount("seller", map[int64]int64{10: 5}, 0)
//	_ = sys.RegisterOrder(settlement.Order{ID: 1, Security: 10,
//	    Buyer: "buyer", Seller: "seller", Qty: 5, Price: 10, SettleDay: 1})
//	_ = sys.RunBatch(1, map[int64]int64{10: 10})
//	view, _ := sys.QueryAccount("buyer")
//	ord, _ := sys.QueryOrder(1)
package settlement
