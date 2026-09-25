# 增量聚合视图维护器 — 设计推导

## 1. 问题建模

基表变更以流式到达：Insert(group, id, value)、Delete(id)、Update(id, group', value')。
视图按 group 维护 Count/Sum/Min/Max/DistinctCount。视图内保存每条记录的索引
`id -> (group, value)`，因此 Delete/Update 只需给出 id，旧值由索引查得。
Update 语义 = 旧组删除 + 新组插入，两组聚合各自维护。

## 2. 各聚合器的撤回策略推导

设组 G 的当前成员多重集为 M，聚合状态 S，删除元素 x。

- **Count**：S = |M|。删 x 后 S' = S - 1，与 M 的其余成员无关。可增量撤回。
- **Sum**：S = ΣM。加法可逆，S' = S - x。可增量撤回。审计按 Float64bits 位级
  比对时，全量重算采用与增量相同的累加顺序（按 id 字典序）以保证确定性。
- **Min**：S = min M。若 x > S，则 S' = S，无需成员；若 x == S，则新极值
  min(M\{x}) 无法从标量状态 S 推出（状态里没有任何关于次小值的信息），
  必须扫描该组成员重算。不可增量撤回（仅当删到极值时）。
- **Max**：与 Min 对偶，x == S 时必须重算。
- **DistinctCount**：S = |{v : v∈M}|。删除 x 后其值 v 是否仍被其他记录持有，
  标量 S 无法回答，需要该值的引用计数；引用计数属于成员级状态，
  故声明「删除需要成员」，用 per-group 的 value->refcount 表维护，
  撤回 = 减计数，计数归零则 distinct 减一。属「需要成员状态才能撤回」，
  与 Min/Max 同列声明 NeedsMembersOnDelete=true，但实现上用引用计数避免全组扫描。

结论：Count/Sum 删除可增量（纯标量逆运算）；Min/Max/DistinctCount 删除时
需要组成员信息，声明 `NeedsMembersOnDelete() = true`，由 view 决定触发
Recompute 阶段（Min/Max 仅在删到当前极值时真正重算，DistinctCount 走引用计数）。

## 3. Recompute 触发条件（例外而非常态）

view 在 Apply 阶段完成校验与日志追加后，进入 Recompute 判定：

- Insert：五种聚合全部可增量，从不触发 Recompute。
- Delete/Update 的删除侧：仅当聚合器声明需要成员、且删掉的值等于该组当前
  极值（Min/Max）时触发重算；DistinctCount 用引用计数 O(1) 撤回，不触发全组
  重算。重算只访问该组成员，访问量 ≤ 组当前成员数，不触碰其他组。
- 组被删空：整组从 `Groups()` 移除，查询返回「不存在」而非零值。

非导出计数器 `recomputeCount[Kind]` 与 `recomputeVisits[Kind]` 记录触发次数与
访问成员数，经 `RecomputeStats()` 只读导出，供测试与 demo 断言。

## 4. 版本单调与幂等判定依据

视图记录 `maxVersion`（已应用的最大版本）与 `lastChange`（该版本对应的变更）。

- v > maxVersion：正常应用。
- v == maxVersion 且变更逐字段等于 lastChange：判定为重复投递，幂等跳过，
  视图不变、不计拒绝。
- v == maxVersion 但内容不同，或 v < maxVersion：判定为乱序/回退，
  返回可判定错误 `ErrVersion`，拒绝计数 +1，视图不被污染。

依据：版本号由上游单调分配，同一版本号唯一标识一条变更；相等版本内容一致
必为重发，内容矛盾或版本回退必为乱序。崩溃恢复重放日志时版本严格递增，
天然满足同一判定。

## 5. 崩溃恢复与多阶段提交

Apply 分三阶段：Apply（校验 + 日志追加，先持久化）→ Recompute（需要时重算）
→ Commit（修改内存状态）。日志先于内存落盘，因此三个崩溃点
（Apply 中途 / Recompute 中途 / Commit 之前）恢复语义为：重放日志即得到
与「已持久化前缀」一致的视图；未入日志的变更视为未发生。恢复后视图与
不崩溃时逐字段相同。

## 6. 日志格式与截断分类

自描述头 8 字节：magic(4) + version(2) + reserved(2)。每条记录：
len(4, 小端) + body(len) + crc32(4, body 的 IEEE CRC)。截断点分类：

- 落在头内：头部不完整；
- 记录槽剩余 < 4：长度前缀不完整；
- 剩余 < 4+len：记录体不完整；
- 剩余 < 4+len+4 或 crc 校验不符：CRC 不匹配。

重放遇到任一错误即停，已解码的完整记录全部生效，半截记录不生效。
