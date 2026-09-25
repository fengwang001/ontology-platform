# 带撤回的增量聚合视图维护器 — 设计

## 1. 数据模型
- 变更 `change.Change`：版本 Version（单调递增 uint64）、操作 Op（Insert/Delete/Update）、
  分组键 Group、新值 Value；Update 额外携带 OldGroup/OldValue（移出旧组、写入新组）。
- Group 为空字符串是合法键；字段缺失用 `GroupSet=false` 表示并拒绝。Value 为 NaN 拒绝。
- 视图按 Group 维护成员集合（记录键 RKey -> 值）与各聚合器状态。

## 2. 聚合器撤回策略推导
- Count：状态是成员条数的整数和，删除贡献恒为 -1，无需成员 => 可撤回。
- Sum：状态是值的代数和，删除减去该值即得新和（浮点减是插入加的逆运算）=> 可撤回。
- Min：状态只保存当前极值。被删值不等于极值时结果不变；等于极值时，仅凭标量无法
  反推次小值（极值不唯一时结果甚至不变），必须扫描该组全部成员重算 => 需成员。
- Max：与 Min 对称 => 需成员。
- DistinctCount：状态是去重值集合；删除某值后，需知道是否还有其他成员持有该值
  （值的频次信息不在标量状态内）=> 需成员，按成员集合重算。
- 接口 `Aggregator` 显式声明 `NeedsMembersOnDelete() bool`；view 仅在删除（或
  Update 的旧组删除）触及声明需要成员的聚合器时触发一次 Recompute。
- Recompute 只重算受影响的单个组，访问成员数上界 = 该组当前成员数，绝不跨组扫描。

## 3. 三阶段与崩溃恢复
Apply：变更先追加写日志（fsync 语义由文件 Sync 保证），再在内存暂存更新；
Recompute：需要时仅对目标组用成员集合全量重算；
Commit：把暂存结果发布到快照（整体替换，读者永不见半更新），推进已应用版本号。
日志帧 = CRC32(头+体) 4 字节 + 二进制头（版本/操作/组长度/值/旧组旧值）+ 体。
文件头固定 magic+version。重放按帧解码；截断分四类：头不完整、CRC 不完整、
（CRC 完整即必然体完整，因 CRC 前置）、CRC 不匹配；乱序帧在 view 层判版本错误。
崩溃点注入：Apply 写日志后、Recompute 中途、Commit 前 panic；重启重放日志
逐条重做完整三阶段 => 结果与不崩溃逐字段相同。

## 4. 版本幂等判定
Commit 记录最大已应用版本 `lastVersion`（初值 0）：
- Version == lastVersion：重复提交，直接幂等返回，视图逐字段不变。
- Version < lastVersion：回退/乱序，返回 `ErrVersionOrder`，计入 Rejected，不触状态。
- Version > lastVersion：正常应用。因此同版本重放天然幂等。

## 5. 并发
单把互斥锁串行化提交（锁内完成 Apply/Recompute/Commit，故同组并发写入不丢）；
读取在锁内做整体快照拷贝后在锁外解读，故不会读到 Count 已更而 Sum 未更的中间态。

## 6. 空组与边界语义
删除使组成员归零时，整体删除该组；查询返回 (nil, false) 的「不存在」而非零值。
NaN 拒绝；+0/-0 按相等处理（Min/Max 比较与 distinct 键用 math.Float64bits 前先归一）。
