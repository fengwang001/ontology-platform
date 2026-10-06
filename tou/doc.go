// Package tou 提供分时电价结算引擎。
//
// 核心模型见 DESIGN.txt。入口为 Engine：
//
//	e := tou.New(nil) // 默认固定 UTC+8
//	e.RegisterTariff(tou.TariffVersion{...})
//	e.SetHoliday("2024-05-01", true)
//	e.RegisterReading("point-1", t1, 0)
//	e.RegisterReading("point-1", t2, 100)
//	bill, _ := e.Bill("point-1", monthStart)
//	e.CloseMonth("point-1", monthStart)
//
// 读数区间内电量视为均匀发生，按日界、月界、时段边界与版本生效时刻切片，
// 各片按时长比例分摊（整数 floor，余量归区间最后一片），按所在日类型、
// 时段与版本单价计价，金额向下取整。错误类别与固定拒绝次序见 types.go。
package tou
