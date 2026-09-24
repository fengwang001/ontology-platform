# DESIGN：带撤回的增量聚合视图维护器

## 1. 数据模型
- `change.Change{Ver int64, Op byte, Group *string, Val float64, ID string, OldGroup *string, OldVal float64}`。
  Op：1 插入、2 删除、3 更新。`Group==nil` 表示分组缺失（拒绝）；空串 `""` 是合法键。
  Val 为 NaN 拒绝。更新携带旧组/旧值，等价于「旧组删旧记录 + 新组插新记录」。
- 组内成员表：`members[group] map[id]value`。删除/更新必须知道被删记录是谁，成员表由重放重建。

## 2. 五个聚合器的撤回策略推导
设组状态仅保存聚合结果，不保存全量成员。
- **Count**：插入 +1、删除 −1，只依赖条数与具体值无关 → 删除可纯增量。
- **Sum**：插入 `+v`、删除 `−v`，被删值由变更携带，线性可逆 → 删除可纯增量。
- **Min**：状态只有当前最小值 m。被删 v≠m 时 m 不变；v==m 时无法知道次小值（同值可能还在）→
  必须遍历成员重算。「需要成员」仅是删除值命中当前极值时的例外，而非常态。
- **Max**：与 Min 对称，v==M 时必须重算。
- **DistinctCount**：状态只有不同值个数，删除 v 后无法知道 v 是否仍被其他记录持有 →
  遍历成员集合重算 distinct。
接口 `Aggregator.NeedsMembers() bool` 静态声明删除是否可能需要成员；view 对返回 true 者再判
触发条件（Min/Max：v==极值；DistinctCount：恒触发）。
非导出计数器：`recomputeCount`（触发次数）、`membersTouched`（访问成员数）。

## 3. Apply → Recompute → Commit
1. Apply（写者锁内、私有暂存）：校验版本/NaN/分组；更新成员表；NeedMembers=false 的聚合直接撤回；
   需重算的组打 dirty 标记。
2. Recompute：仅遍历 dirty 组、只遍历该组自己的成员（访问数 ≤ 该组成员数，不扫描其他组），
   算出 Min/Max/DistinctCount。组内无成员则整组删除（不留 Count=0 空组）。
3. Commit：指针替换原子发布新 GroupState，更新 maxVersion。读者只见旧或新状态，
   不可能看到「Count 已变、Sum 未变」。
并发：`sync.RWMutex` 串行化写者、读者取快照；Recompute 在写者锁内完成，期间写入不丢。

## 4. 版本幂等
视图记录 `maxVersion`：
- `Ver == maxVersion` 且内容一致 → 幂等跳过，状态逐字段不变；
- `Ver < maxVersion` → 可判定错误 `ErrVersionBackward`，拒绝计数；
- `Ver > maxVersion+1` 乱序 → 拒绝计数，视图不被污染。

## 5. 日志格式、截断分类与崩溃恢复
文件 = 头 `"IVJ1"` + 4 字节大端 payloadLen，随后每条记录：
`[4 字节 bodyLen(大端)][body][4 字节 CRC32-IEEE(body)]`，body 为 change 自描述编码。
按已读字节位置分类：头不满 8 字节 `ErrShortHeader`；长度前缀不满 `ErrShortLength`；
body 不满 `ErrShortBody`；CRC 不满 `ErrShortCRC`；CRC 不符 `ErrCRC`。
半截记录不生效，重放结果恒等于「完整记录前缀」全量重算。
三个崩溃点（Apply 中 / Recompute 中 / Commit 前）都在发布前：日志已 fsync，内存未提交；
重启重放完整日志后逐字段等于不崩溃运行（提交成功以日志为准）。

## 6. 全量重算核对（audit）
收集全部已接受变更，按版本升序、以与视图同构规则纯函数重算每组五列；Sum 严格按同一顺序累加，
用 `math.Float64bits` 做 IEEE754 位级比对；+0/−0 视为相等。
