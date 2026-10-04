# 路由抖动抑制器设计说明

1. 三包分层：`decay` 只做纯整数算术（一步衰减、结算、求 j、Z）；`reuse` 只管按 reuseAt 排序的最小堆与重算更新；`damp` 持有每路由记录、锁、时钟与状态机。
2. 衰减基准 `last` 每次结算只加 `k·Δ`（k=⌊(t-last)/Δ⌋），不置为当前时刻：相位锚定记录建立时刻，保证 reuseAt=last+j·Δ 可精确复现（题例 65 与误实现 66 之别）。
3. 长间隔不逐步进：先算 k，再逐步取整至多 Z 步（p 一旦到 0 不再变化）；放弃“一次乘 num^k/den^k”方案，因其与逐步取整语义不同。
4. 求 j（衰减后严格小于 Pr 的最小正步数）同样用循环逐步计算，上界 Z；故每次结算/求 reuseAt 的乘法步数 ≤ Z+1，`mulSteps` 计数随调用重置，无浮点、无幂近似。
5. Z 在构造时按定义从 Pmax 逐步衰减到 0 算一次。
6. 抑制状态只在“结算→加罚 clamp 到 Pmax”之后判定：未抑制且 p≥Ps（含恰等）则抑制，supSince=now；已抑制则 supSince 不动，仅按新 (p,last) 重算 reuseAt。
7. reuseAt=min(supSince+Tmax, last+j·Δ)；j 基于加罚后结算到 now 的 (p,last)。Tmax 到期解除不改 p/last，此后无新罚不会重新抑制。
8. 每个被接受操作开始先按 (reuseAt, peer, prefix) 弹出 reuseAt≤now（含恰等）的路由，Tmax 解除不结算；再执行操作语义。因此恰在 reuseAt 的加罚先解除、再加罚、重判。
9. 事件 Usable = 解除后该路由 advertised 且未抑制。
10. 拒绝优先级：参数非法 > 时钟回退（now<已接受最大 now）> 路由/邻居不存在；拒绝不解除、不推进时钟、无副作用。
11. Penalty 只读：结算到 t 的临时 (p,suppressed) 副本（含到期 Tmax 解除的语义判断），不落地；无记录返回 (0,false)。
12. PeerDown 将该邻居所有 advertised 路由置为撤销、不加罚、不结算、不碰抑制；无 advertised 路由报邻居不存在。
13. 首次受罚才建记录（last=now,p=0）；首次 Announce 仅建路由态（advertised=true），无惩罚记录。
14. 并发：damp 内单 mutex 包住“解除+操作”全过程；结果等价于某串行顺序。
15. 被放弃：预打衰减表（Z 最坏可达 ~1e9 量级，不可行）；per-route 定时器（破坏纯函数式解除与可重放性）。
16. 本地验证：`go test ./...`；边界表驱动用例 + 1500 组随机序列与逐毫秒朴素模拟对照，日志打印输入/输出/判定依据；`go test -race` 验证并发。
