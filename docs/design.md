# 实例存储与跨类型聚合可见性子系统 — 设计说明

## 1. 目标与非目标

实现一个进程内的本体实例存储，满足：

- 实例写入的**乐观并发**：以前序版本号为凭证，凭证不匹配即拒绝，且拒绝不消耗版本号。
- 聚合视图是源实例的**纯粹派生物**：不单独持久化成员快照；任何时刻查询等价于用当时最新可见源实例重新计算。
- 一次提交对其涉及的**多个聚合视图整体可见**，提交生效点即可见点，无额外时延。
- 删除语义区分「从未提交过 → `NOT_FOUND`」与「已删除后再删 → `VERSION_CONFLICT/ALREADY_DELETED`」。
- 分组键迁移（旧组扣除 / 新组计入）对查询者是**一个不可分割事件**。
- 拒绝原因严格按序只报第一个：参数非法 → 凭证不匹配 → 删除不存在。
- 并发操作的最终效果等价于某个全局串行顺序，且可重放复现。
- 单个分组查询开销只与该分组成员规模相关，与类型下实例总数无关。

非目标：跨进程持久化、网络协议、除 sum/count 外的聚合函数、删除后同主键复用（见 §5 取舍）。

## 2. 模块划分

三个模块对应题面要求，文件都在 `ontology/` 包内：

| 职责 | 文件 | 关键类型 |
| --- | --- | --- |
| 实例版本仲裁 | `ontology/versioning.go` | `Arbiter`、`Decision` |
| 聚合视图增量维护 | `ontology/aggregate.go` | `Maintainer`、`viewIndex`、`groupIndex` |
| 一致性校验与错误归一化 | `ontology/consistency.go`、`ontology/errors.go` | `Validator`、`OpError` |

编排与存储在 `ontology/store.go`；独立朴素对照模型在 `ontology/naive.go`。

### 2.1 版本仲裁（Arbiter）

`Arbiter` 是**无状态纯函数模块**。它不自己持有锁：调用方（Store）已持有全局
互斥锁，锁本身即唯一串行化点。仲裁规则：

- 写 `expected==0`（创建）：仅当该主键从未提交过任何版本时接受；否则冲突。
- 写 `expected>0`（更新）：记录存活且 `expected == 当前版本` 才接受；
  记录不存在/已删除 → `WRITE_ON_MISSING`；版本不等 → `STALE_VERSION`。
- 删：从未提交 → 交由 Store 归为 `NOT_FOUND`；已在墓碑上 → `ALREADY_DELETED`；
  `expected>0` 且与当前存活版本不等 → `STALE_VERSION`；`expected==0` 匹配任意存活版本。

因为仲裁与状态推进在同一临界区，失败路径在任何状态变更**之前**返回，
所以拒绝绝不推进 `Version`，也不触碰任何聚合索引。

### 2.2 聚合增量维护（Maintainer）

派生结构（不是第二份事实来源）：

```
view -> group(string) -> { memberRef(type,key) -> contribution(float64) }
```

- `ApplyWrite(inst, prev)` 对实例所属的**每个视图、每个源**计算 `(旧组, 新组)`，
  在一次调用内完成「旧组移除 + 新组插入」。组变了就是迁移，组没变就是原地更新。
- 组在成员归零时从 map 删除，所以查询不存在的组直接得到空结果，无残留。
- `ApplyDelete` 把实例标记为墓碑后复用 `ApplyWrite`（新贡献为空），
  从而「扣除所有视图贡献」与删除同处一个临界区。
- `Query` 只遍历**该分组的成员 map** 求和/计数，复杂度 `O(该分组成员数)`。

派生索引只作为性能缓存；`Validator.Verify` 随时可从源快照全量重算并逐项比对，
证明查询等价于「用最新可见版本重新计算」。

### 2.3 校验与归一化（Validator / OpError）

所有错误统一成 `OpError{Code, Reason, ...}`，三个 `Code` 互可区分：

1. `INVALID_ARGUMENT`：类型/主键空、未知类型、必填缺失、类型不符、作为强制
   分组键的属性为空。
2. `VERSION_CONFLICT`（含 `STALE_VERSION` / `WRITE_ON_MISSING` / `ALREADY_DELETED`）。
3. `NOT_FOUND`：删除一个从未提交过的主键。

Store 严格按序：先做参数校验（无锁），再加锁做版本仲裁，删除时最后判定
`NOT_FOUND`。因此同一操作即使同时命中多类问题，也只报第一类。

## 3. 原子性与可见性：关键取舍

**单把全局 `sync.RWMutex`，一次提交 = 一个临界区。**

一次写在临界区内依次完成：仲裁 → 更新实例表（版本+1）→ 对所有受影响视图做
`ApplyWrite` → 追加日志。查询取读锁。因此：

- 多视图整体可见：不可能读到「视图 A 已更新、视图 B 仍旧」，因为它们在同一次
  写锁更新内，任何读锁要么看到之前要么看到之后。
- 迁移不可分割：旧组扣除与新组计入是同一次 map 操作序列，读锁无法夹在中间。
- 删除扣除与生效同一时刻，且不残留空组。
- 生效点即锁内状态切换点，没有异步队列、没有「延迟发布」窗口。

读路径用 `RLock`，因此查询彼此并发、与写互斥；查询分组只遍历其成员 map。

## 4. 串行化与可重放

每个被接受的提交追加一条 `JournalEntry`，带全局单调 `Sequence`。`Replay` 在空
Store 上按序重放日志（写的凭证用 `Version-1` 重建，删除用任意存活版本）。
由于仲裁是确定的纯函数、提交顺序即锁获取顺序，重放得到逐位相同的各聚合取值。
测试以「并发跑完 → 重放日志 → 比对所有分组」作为**串行化存在性见证**：
能被某条串行序列精确重现，即证明并发效果等价于某个全局串行顺序。

## 5. 被放弃 / 明确不做的方案

- **每视图独立锁 / 两阶段跨视图提交**：需要跨锁原子提交或 2PC，否则多视图
  必然存在中间态。为满足「无额外生效时延的整体可见」，放弃细粒度锁，改用单一
  串行化点。代价是写吞吐受限；本系统语义优先，且查询仍可并发。
- **持久化成员列表快照**：会引入「快照与源不一致」的窗口和修复负担，违背
  「纯粹派生物」。只保留可随时丢弃并用 `Verify` 校验的派生索引。
- **异步增量队列**：提交与生效之间出现时延，直接违反要求，放弃。
- **删除后允许同主键重新创建**：会让「再删」与「新建后再删」的错误归类歧义。
  当前墓碑主键永久占用，再写冲突、再删为 `ALREADY_DELETED`，语义最清晰。
  未来若需要复用主键，应显式引入代际（generation）令牌。
- **除 sum/count 外的聚合**：接口已按 `AggKind` 预留，但增量可逆性对
  min/max/avg/distinct 需要额外结构，不在本次范围。

## 6. 数据模型要点

- 属性类型：`int` / `float` / `string`；校验要求精确匹配（int 属性传 string 即非法）。
- 分组键：以属性值规范化成字符串；强制分组键为空属于参数非法。
- sum 视图按 `ValueAttr` 数值求和；count 视图贡献恒为 1。
- 视图可声明多个源对象类型，同组键跨类型合并（跨类型聚合）。

## 7. 本地验证方法

```bash
# 环境（若 go 不在 PATH）
export PATH=$PATH:/usr/local/go/bin

# 全量 + 竞态检测
go test -race ./...

# 更大规模随机差分（朴素全扫模型逐条对照）
go test ./... -run TestRandomDifferential -v -ds-seeds=40 -ds-ops=600

# 查询复杂度证明（总实例翻倍、分组成员数固定）
go test ./... -run TestQueryComplexity -v

go vet ./...
gofmt -l .
```

### 测试与需求对应

| 需求 | 测试 |
| --- | --- |
| 多视图整体可见 | `TestMultiViewAtomicVisibility`、`TestConcurrentReaderAtomicSnapshot` |
| 迁移互斥归属 | `TestGroupMigrationMutualExclusion` |
| 未提交删除 vs 再删除 | `TestDeleteSemantics` |
| 错误优先级 / 三类可区分 | `TestErrorPriorityOrdering` |
| 拒绝不占版本、不改聚合 | `TestRejectedOpConsumesNoVersion` |
| 乐观凭证交错竞争 | `TestOptimisticInterleaving`、`TestConcurrentSerializability` |
| 串行等价 + 重放确定性 | `TestReplayDeterminism`、`TestConcurrentSerializability` |
| 查询与总数无关 | `TestQueryComplexityIndependentOfTotal` |
| 随机写入/删除/迁移逐条对照朴素模型 | `TestRandomDifferential` |

每次操作通过 `t.Logf` 打印「输入 / 实际输出 / 判定依据」，用 `-v` 可见。
`Verify` 在差分过程中周期性执行，从源快照全量重算并与增量索引比对。
