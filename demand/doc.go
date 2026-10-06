// Package demand 实现工业用户最大需量控制器。
//
// 它按滑动窗口平均功率逼近合同需量，在预测越限时按优先级切除可控负荷，
// 并在恢复时遵守最短接入/最短断开约束。评估开销只与负荷数及重叠窗口数
// 相关，不随历史上报次数增长；所有公开方法并发安全。
//
// 典型用法：
//
//	c, err := demand.New(demand.Config{
//	    ContractKW: 100, // 合同需量（千瓦）
//	    WindowSec:  60,  // 窗口长度（秒）
//	    SlipSec:    30,  // 滑差（秒），窗口长度须为其整数倍
//	    MaxPowerKW: 1000,
//	}, demand.WithLogger(logger))
//
//	_ = c.AddLoad(0, demand.LoadSpec{
//	    ID: 1, RatedKW: 40, Priority: 2, MinOnSec: 30, MinOffSec: 30,
//	})
//
//	res, err := c.Report(30, 3000) // 时刻30s、用电量3000千瓦秒（100kW）
//	res.Actions                      // 本次切除/恢复动作
//	c.Peak()                         // 历史最高实测需量及窗口结束时刻
//
// 错误以 *demand.Error 返回，其 Kind 字段区分五类错误，类别次序见
// DESIGN.md。运维操作 AddLoad/LockLoad/UnlockLoad/RemoveLoad 不触发
// 评估，只影响后续评估。
package demand
