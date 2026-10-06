// Package dispatch 实现即时配送骑手“顺路合并”派单：
// 路线推定、插入可行性与确定性骑手选择、订单生命周期与并发协调。
//
// # 典型用法
//
//	tt := dispatch.TravelMap{...}                 // 或任意实现 TravelTimeSource 的耗时源
//	d := dispatch.NewDispatcher(tt, dispatch.Config{
//	    PickupDwell: 30, DropDwell: 10, MaxDetour: 120,
//	})
//	_ = d.RegisterRider(0, dispatch.Rider{ID: "r1", Region: "M", Capacity: 4, Pos: "H"})
//	_ = d.SubmitOrder(1, dispatch.Order{ID: "o1", Pickup: "A", Dropoff: "B",
//	    ReadyAt: 60, Promise: 600})
//	res, err := d.DispatchOrder(2, "o1")        // 确定性骑手与插入位置
//
// 错误按固定次序拒绝：参数非法、时钟回退、对象/状态不符、无可行骑手（三种可区分原因）。
// 所有方法并发安全，结果等价于某个串行顺序。
package dispatch
