// Package narledger 实现医院麻醉与精神类药品专用账册：
// 入库登记、双人复核领用、使用后闭环结清、差额锁定与销毁见证。
//
// 核心入口是 Ledger，全部方法并发安全。判定规则、性能取舍与
// 本地验证方法见 narledger/DESIGN.md。
package narledger
