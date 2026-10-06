// Package settlement 实现光伏余电上网的月度结算引擎。
//
// 核心抽象：
//   - Engine：线程安全入口，持有若干产消者；所有公开方法可并发调用，
//     语义等价于某一串行执行顺序。
//   - Params / Prices：按月生效的参数与单价阶梯表（SetParams/SetPrices），
//     生效月只能指定尚未封账的月份。
//   - Reading：某计量间隔的（下网 Wh, 上网 Wh）登记；同间隔重复且两项相同
//     为幂等，不同且未封账为修正，已封账返回 ErrClosed。
//   - CloseMonth：按 非法 → 已封账 → 顺序错误 → 数据缺失 的固定次序校验后
//     封账；数据缺失错误携带最早缺失的间隔起点。
//   - Query：已封账月返回冻结结果；未封账月按当前数据与参数试算，
//     反映最新状态但不改变持久额度余额。
//
// 单位约定：电量 Wh（整数）、功率 W（整数）、单价 分/Wh（整数），金额整数。
// 时间一律按 UTC 解释；间隔网格由 NewEngine 的 intervalSeconds 与 epoch 定义，
// intervalSeconds 必须为正且整除 86400。
//
// 错误判定：
//
//	errors.Is(err, settlement.ErrInvalid|ErrClosed|ErrOrder|ErrMissing)
//
// 或 errors.As 取得 *SettlementError，读取 Kind 以及（数据缺失时的）最早缺失间隔。
package settlement
