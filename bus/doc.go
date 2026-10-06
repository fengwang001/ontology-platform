/*
Package bus 实现一条公交线路的车辆排班与串车调整服务。

# 建模

线路由有序站点构成，控制站在 Scheme.Stops[i].Control 中标记；
目标发车间隔、扣车上限、离站容忍量、工时上限均为正整数秒。
每辆车绑定一名预先登记的司机，车次号的先后即计划发车次序。

# 操作

典型调用顺序：

	svc, _ := bus.NewService(sc)
	var log bytes.Buffer
	svc.SetLogger(&log)                 // 可选：打印每条操作的判定依据
	_ = svc.RegisterDriver("d1", 0)
	_ = svc.RegisterTrip(100, "d1", 1)  // 车次号必须严格递增
	_ = svc.AddSpare(50, "d2", 2)       // 备车入池
	ev, err := svc.ReportArrival(100, 0, 100)

车辆到达控制站的瞬间自动完成串车判定与处置：

  - gap < H/2（取等不算）判串车：先尝试扣车；扣车会越过最晚允许离站则改判
    跳站；扣车会越过工时上限则拒绝并降级为不干预（不改判跳站）。
  - gap > 2H（取等不算）且备车池非空：取最小号备车，以前车到站+H 在本站插入。
  - 其余情形只记录到站。

查询使用 GetEvent(tripID, stop)，O(1) 返回到离时刻、干预类别与判定/降级原因；
RequestIntervention 可对串车站点查询最终生效的干预，对象不是串车时返回
ErrNotBunched。

# 并发与复现

全部入口由单把互斥锁保护，并发调用等价于某个全局接受时钟下的串行顺序；
被拒绝的操作在任何状态写入之前返回，因此不改变状态、车次次序与时钟。
同一操作序列在 NaiveModel 上独立重放，可逐事件交叉验证；SetLogger 的输出
用于人工审计与“相同序列完全复现”的测试。
*/
package bus
