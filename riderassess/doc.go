// Package riderassess 实现骑手考核与申诉系统。
//
// 平台按固定周期记录骑手的扣分事件，周期结束时按累计扣分定级并决定下一周期
// 的接单（权益）等级；骑手可申诉，申诉成立连带撤销同根因簇内全部事件，并按
// 受影响周期是否已结算决定是直接重算还是补偿+回溯改写下降基准。
//
// 典型用法：
//
//	sys, err := riderassess.New(cfg)
//	sys.RegisterRider(0, "rider-1")
//	sys.RegisterEvent(1, riderassess.Event{ID:"e1", Rider:"rider-1", OccurAt:5,
//	    Type: riderassess.TypeLateDelivery, RootCause:"merchant-7"})
//	rep, err := sys.QueryPeriod(100, "rider-1", 0) // 右端点后查询即结算
//	sys.FileAppeal(120, "appeal-1", "e1")
//	sys.RuleAppeal(130, "appeal-1", true)
//
// 所有方法可被多 goroutine 并发调用，结果等价于某个串行顺序；非法操作返回
// *riderassess.Error，可通过其 Code 字段程序化区分错误类型。
package riderassess
