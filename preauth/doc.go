// Package preauth 实现支付预授权（authorization hold）的额度账务：
// 初始持有、增量授权、分次/超额捕获、终捕、撤销与退款，并保证持卡人可用
// 额度在授权过期、撤销、超额捕获等情形下始终可精确复现。
//
// 核心恒等式：
//
//	available = credit - posted - Σ activeRemainingHold
//
// 所有金额为正整数最小货币单位，时间为整数天。每个操作携带 now，now 小于
// 该账户上一次被接受操作的 now 即返回 ErrClockBackward；被拒绝操作不改变
// 任何状态与时钟。过期在创建/增量日起 ValidityDays 天内（含第 E 天）有效，
// 次日自动失效，过期释放不依赖后台任务，而由查询/被接受操作惰性折叠。
//
// 典型用法：
//
//	sys := preauth.NewSystem(preauth.Config{ValidityDays: 7, ToleranceBPS: 1000})
//	_ = sys.CreateAccount("card-1", 10000, 0)
//	_ = sys.Authorize("auth-1", "card-1", 3000, 1)
//	v, _ := sys.Available("card-1", 1)        // 7000
//	_ = sys.Increment("auth-1", 500, 3)       // 累计授权 3500，重置有效期
//	_ = sys.Capture("auth-1", 3300, false, 4) // 分次捕获
//	_ = sys.Capture("auth-1", 300, true, 5)   // 终捕并释放剩余持有
//	_ = sys.Refund("auth-1", 200, 6)          // 退款，不恢复持有
//
// 错误用 errors.Is 区分；优先级见各方法注释与 docs/design.md。
package preauth
