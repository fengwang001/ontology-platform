// Package retention 实现本体平台上对象的逻辑删除与可见性服务。
//
// # 状态机
//
// 四种互斥状态与唯一允许的转换方向：
//
//	ALIVE ──soft_delete──▶ GRACE ──undelete──▶ ALIVE
//	  │                     │
//	  │                     └──(graceDeadline 到达，下一次操作自动)──▶ ARCHIVED
//	  └──freeze(d)──▶ FROZEN ──(freezeDeadline 到达后显式 archive)──▶ ARCHIVED
//
// ARCHIVED 是终态。任何违反方向的请求返回 ErrIllegalTransition，
// 且不修改状态、截止时刻与时钟。
//
// # 宽限期与冻结期
//
// soft_delete 记录 graceDeadline（必须严格晚于当前时刻）；在
// graceDeadline 之前 undelete 使对象回到 ALIVE 并清除本次删除痕迹。
// now >= graceDeadline 时，下一次任何涉及该对象的操作（含查询，
// 也含被拒绝的转换请求）先把对象自动归档（auto_archive_after_grace），
// 该转换不消耗调用方请求。
//
// freeze(d) 在进入时一次性确定 freezeDeadline = now + d；冻结期间
// 既不能撤销也不能显式归档，未满足时返回 ErrFrozenNotExpired。
//
// # 可见性
//
//   - RoleUser：仅 ALIVE 可见；其余状态一律视为“不存在”，不泄露状态。
//   - RoleAdmin：四种状态均可见；FROZEN 时只返回状态与 freezeDeadline，
//     业务属性被遮蔽（Attributes 为 nil）。
//   - 出边链接没有自己的删除状态，可见性完全跟随源对象。
//     FROZEN 管理员视图下，出边作为业务关系数据与业务属性一并遮蔽。
//
// # 错误次序
//
// 四类错误互斥，判定次序固定：
//
//	ErrNotFound → ErrIllegalTransition → ErrInvalidParameter
//	→ ErrFrozenNotExpired
//
// 唯一的次序细节：undelete/archive 对“FROZEN 且未满”优先报
// ErrFrozenNotExpired（第四类），因为 FROZEN→ALIVE/ARCHIVED 的
// 方向本身由“冻结未满”这一更具体的守卫拒绝。
//
// # 并发与历史复杂度
//
// 单把 sync.RWMutex 串行化写、并为查询提供单一时间点快照；
// QueryWithEdges 在同一临界区内判定对象与全部出边。
// 每个对象只保存当前态与至多一个截止时刻，不保留历史，
// 故可见性判定触及的状态记录恒为 1 条（O(1)），由探针
// ProbeCount 在测试中复现核对。
package retention
