// Package capture 实现闭包捕获变量的开闭管理器。
//
// 它模拟“闭包按引用捕获局部变量、作用域退出时变量关闭”的语义：
// 捕获变量在开放期与值栈槽共享同一存储，作用域关闭时把槽值快照到自身，
// 此后与栈完全脱钩。
//
// # 开放与关闭状态
//
// 槽号从 0 起。Push 在栈顶压入值并返回新槽号，栈顶加一。
// Capture(slot) 返回该槽上的捕获变量句柄：
//   - 同一槽上已有开放的捕获变量时，返回同一句柄（共享），每次捕获使持有数加一；
//   - 否则新建句柄。句柄编号从 1 起，全局递增且永不复用。
//
// 开放变量不持有自身存储：HandleGet/HandleSet 直接读写对应栈槽，
// SlotGet/SlotSet 对它完全可见，反之亦然。
//
// CloseFrom(level) 关闭槽号不小于 level 的全部开放变量：先把当前槽值
// 复制进各自的本地存储、标记为关闭并从共享表摘除，再把栈顶设为 level。
// level 恰等于栈顶是合法空操作；关闭后的 HandleGet/HandleSet 只作用于
// 本地存储，栈槽的后续变化（包括同槽重新压栈）对其不可见。
//
// # 持有数与摘除规则
//
// 每次 Capture（含复用同一句柄）使持有数加一，每次 Release 减一。
// 持有数降到 0 时：
//   - 变量仍开放：立即从共享表摘除。该槽之后再次 Capture 得到全新句柄；
//   - 变量已关闭：无需摘表（关闭时已摘除），本地存储保留但不再可达。
//
// 持有数为 0 的句柄再做 HandleGet/HandleSet/Release 一律无效，
// 返回 ReasonHandleReleased；从未分配过的编号返回 ReasonHandleNotFound，
// 两类原因互斥可区分。
//
// # 拒绝规则
//
// 以下操作整体拒绝，且不改变栈、栈顶与任何捕获变量：
//   - Capture 的槽号为负或不小于栈顶（ReasonCaptureSlotAboveTop）；
//   - CloseFrom 的 level 为负或大于栈顶（ReasonCloseLevelOutOfRange）；
//   - SlotGet/SlotSet 的槽号为负或不小于栈顶（ReasonSlotOutOfRange）；
//   - 句柄不存在（ReasonHandleNotFound）或已释放完（ReasonHandleReleased）。
//
// # 并发与可复现性
//
// 全部方法可用同一 Manager 并发调用；单把互斥锁使每个操作原子化，
// 任意并发历史都等价于某个串行顺序（线性一致）。任何时刻同一槽至多
// 存在一个共享的开放变量，关闭变量不再受栈修改影响。句柄编号只取决于
// 操作序列，因此相同序列在全新管理器上重放，得到完全相同的句柄与取值。
package capture
