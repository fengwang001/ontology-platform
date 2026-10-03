# 滚动升级元数据格式兼容闸门 — 设计说明

## 数据与职责
- cluster：节点注册表（name→max，max∈[1,3]），提供加锁的 Join/Leave/Snapshot/MinMax。
- meta：key→记录，记录含 level 与 size/etag/tags（区分“缺省”与空值：指针为 nil 即缺省）；按级别维护计数 countByLevel，覆盖写旧级别减一、新级别加一，Delete 减一。
- gate：持有一把 RWMutex（写操作互斥），G 初值 1；编排 Put/Get/Delete/Raise/Rollback。

## 取舍理由
- 拒绝降级节点写入（node.max<G 直接报只读），而不是按其 max 低级别写入：低级别写入会产生级别低于 G 的永久记录，破坏“任何记录级别≤G 且 Put 级别恰为当时 G”的不变量，也使后续 Raise 无法判断集群真实进度。
- 提供了最低级别>G 的字段时拒绝，而不是静默丢弃：静默丢弃会让调用方误以为 etag/tags 已持久化，读回缺失时无法与“本来就没传”区分，属不可见数据损失。
- Raise 只允许 target==G+1：多级跳升无法在中途失败时精确界定哪些节点已就绪；单步提升使两阶段 Ack/Release 的撤销边界唯一、可复现。
- Rollback 检查残留（存在 level>target 的记录即拒绝），而不是有损重写/降级记录：降级重写必须丢弃高级别字段，语义不可逆；残留检查零扫描（直接读 countByLevel），被拒操作不动任何状态。

## 并发与原子性
- 所有变更操作取同一把互斥锁，天然等价于某串行顺序。
- Raise 在锁内对“调用时刻在册快照”按名字字节序 Ack，失败则对已成功者逆序 Release（返回值忽略），G 与全部状态不变；故期间的 Join/Leave/Put 只能排在整个 Raise 之前或之后。
- Ack 失败信息仅由 gate 组装为“确认失败”；Ack 不得回调本服务（注入方职责，测试用 fake 保证）。

## 错误次序
- Join：参数非法（name 空、max 越界）优先于“已存在”。Leave：参数非法优先于“不存在”。
- Put：参数非法 > 节点不在册 > 节点只读 > 字段不支持。Get/Delete：参数非法 > 节点不在册 > 键不存在。
- Raise：target≠G+1 或 >3 参数非法（G=1 时 Raise(3)/Raise(1) 均非法）> 无节点 > 节点落后（max 最小者，并列名字字节序最小）> 确认失败。

## 本地验证
- `go test ./... -race -v`：表驱动用例（题目三个示例、边界 max、残留=target/=target+1、缺省 vs 空串、Ack 首/中/末失败）。
- `go test -run TestRandomReplay`：1500 组随机序列与逐步朴素模型逐步对照，含随机 Ack 失败，日志打印输入、输出与判定依据；校验 Ack/Release 调用序列、scanned 恒为 0、重放确定性。
