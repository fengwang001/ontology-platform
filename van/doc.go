// Package van 实现厢式货车的配载与卸货顺序校验系统。
//
// 车厢由从车头到车尾编号 1..Z 的分区组成，每区有载重（克）与容积（cm³）上限。
// 货物（Cargo）带编号、重量、体积、卸货停靠点序号与类别。系统在同时满足
// 顺序（后卸靠前）、危险品隔离、载重、容积四类约束时，把单件货物放入编号最小的
// 可行分区；无可行分区时给出可区分、按严重度归并的失败原因。
//
// 典型用法：
//
//	s := van.New([]van.ZoneSpec{{WeightLimit: 100, VolumeLimit: 100}})
//	zoneNo, err := s.Load(van.Cargo{ID: 1, Weight: 10, Volume: 10, Stop: 2})
//	ids, err := s.Unload(2)
//	rem := s.Remaining()
//
// 所有方法可并发调用；查询返回某个已完成操作之后的一致快照副本。
// 失败错误可用 errors.Is 区分 ErrInvalid/ErrDuplicate/ErrStopPassed/
// ErrOrderError/ErrProcessed/ErrNotFound，四类约束失败用 *RejectError 并以
// errors.As 取得 RejectReason。批量失败返回 *BatchError（含最小下标与原因）。
package van
