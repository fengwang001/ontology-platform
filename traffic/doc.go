// Package traffic 实现带冷却隔离与世代重盐（re-salt）的互斥实验层流量编排器。
//
// # 构造
//
//   - B：桶总数，2..1_000_000。
//   - H：保留桶数，0..B-1；桶 0..H-1 永久为对照组，永不分配、永不扫描。
//   - Cd：冷却期，0..1e9。
//   - P：盐步长，1..1e6。
//
// 所有带时钟操作的 now 均为 0..1e15 的 int64；世代 g 初值 0，上限 1e6。
//
// # 桶三态与冷却可用规则
//
// 桶 H..B-1 处于三态之一：
//
//   - 空闲（StateFree）：从未分配，或已被 Reshuffle 重盐清空。
//   - 占用（StateHeld）：属于某个实验，区间连续且实验间互不相交。
//   - 冷却（StateCooldown）：记录上一任所有者 o 与释放时刻 r。
//
// 每桶另记冷却次数 k（初值 0）：每次进入冷却 k+1；桶被重新分配（含被
// 上一任本人收回）时 k 保留；Reshuffle 时所有桶 k 清零。
//
// 冷却桶的可用性：
//
//   - 对 o 本人：始终立即可用。
//   - 对其他实验：now >= r + Cd*min(k,3) 才可用（恰等可用；Cd=0 时释放即可用）。
//
// 已过期但未被重新分配的冷却桶仍计入 Usage.Cooldown，并继续记着 o、r。
//
// # 选段与扩缩规则
//
//   - Claim(id,n,now)：从 H 起单次扫描，取起点最小的、整段 n 个桶都对 id
//     可用的位置；任一桶不可用即截断当前连续段。找不到返回 ErrCapacity。
//   - Resize(id,n2,now)：
//   - n2 等于现有个数：空操作（仍校验时钟并推进最大 now）。
//   - n2 更小：保留起点起前 n2 个桶，其余尾部桶进入冷却（o=id，r=now，k+1）。
//   - n2 更大：只能向右原地延伸，紧邻右侧所需桶须全部对 id 可用；
//     越界、遇占用或不可用冷却桶均返回 ErrCapacity，绝不搬迁。
//   - Release(id,now)：实验全部桶进入冷却并注销实验；同 id 之后可重新 Claim。
//
// # 世代重盐与映射
//
// Reshuffle(now)：g 加一；所有冷却桶立即变为空闲（用户已被重新打散）；
// 所有桶（含占用桶）k 清零；占用关系不变。g 已为 1e6 时返回
// ErrGenerationExhausted。
//
// Lookup 映射公式（O(1) 直接索引，不扫描桶表）：
//
//	bucket(h, g) = (h + g*P) mod B
//
// bucket < H 返回对照；空闲返回无实验；冷却返回无实验并附带 o 与对他人
// 可用时刻 r+Cd*min(k,3)；占用返回所属实验与桶号。Lookup 不接收 now，
// 因此不做时钟回退检查。
//
// # 拒绝优先级
//
// 按以下顺序只返回第一个原因，被拒绝操作不改变任何状态：
//
//  1. 参数非法（ErrInvalidArgument）
//  2. 实验已存在（ErrExperimentExists，仅 Claim）
//  3. 实验不存在（ErrExperimentNotFound，仅 Resize/Release）
//  4. 时钟回退（ErrClockRewound，now 小于已接受操作的最大 now）
//  5. 世代耗尽（ErrGenerationExhausted，仅 Reshuffle）
//  6. 容量不足（ErrCapacity）
//
// # 并发与复杂度
//
// 写操作互斥、Lookup/Usage/Generation 用读锁共享；Lookup 不会观察到
// Resize 或 Reshuffle 的中间状态，所有方法并发调用等价于某一串行顺序。
// Claim 与扩容的选段扫描对桶表只走一遍（非导出计数器
// Orchestrator.lastScanExamined 供测试验证，考察桶数不超过 B-H）。
// 相同操作序列重放得到完全相同的桶表、世代与 Lookup 结果。
package traffic
