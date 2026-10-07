# 派生属性索引一致性子系统 — 设计说明

代码位于 `derived/` 包（module `ontology`）。本文件说明关键模型、取舍、
被放弃的方案与本地验证方法。

## 1. 问题定义与形式化

- 对象实例：`Object{ID, Type, Properties}`。
- 有向实例链接：`from --Type--> to`，按三元组去重。
- 派生索引声明 `Declaration{Name, DownstreamType, LinkType, SourceType,
  SourceProperty, RequireUnique}`：
  - 下游实例 `x` 的索引键，等于 `x` 经 `LinkType` 唯一连接到的实例
    在 `SourceProperty` 上的当前值；
  - `SourceProperty` 可以是源对象的**基属性**，也可以是**另一个声明名**，
    后者构成多级传递派生；
  - `RequireUnique=true` 时，若该类型链接多于一条，条目进入明确的
    `NOT_UNIQUE` 不可索引状态，绝不任选其一。

条目状态集合：

| 状态 | 含义 |
| --- | --- |
| `INDEXED` | `Keys` 为当前解析出的键（非唯一声明可含多个键） |
| `NO_LINK` | 该类型链接一条都不存在 |
| `NOT_UNIQUE` | 要求唯一但存在多条链接 |
| `NO_VALUE` | 链接存在但源端不提供该属性（含中间链断裂） |
| `SOURCE_GONE` | 链接仍指向已删除实例（防御性状态，正常删除流程中先拆链，实际观测到的是 `NO_LINK`） |

## 2. 关键取舍

### 2.1 不做物化索引，条目始终由“真值快照”重算

派生条目从不作为可独立修改的数据存储；每次查询与每次变更后的条目都由
当前对象表 + 链接表纯函数计算（`resolveEffective`/`computeEntry`）。

- 优点：结构上不可能出现“索引更新漏了一个源变化”这类陈旧不一致；
  并发时只需保护一个真值快照，线性化点单一。
- 代价：查询需要沿链解析。本项目规模下这是刻意的简单性选择；生产化时
  可在快照内叠加只读缓存（缓存失效集恰好等于下面的 BFS 受影响集），
  一致性证明不变。

### 2.2 处理单元 = 快照上的 COW 事务

每次变更在全局互斥区内：

1. 深拷贝当前快照；
2. 在候选快照上做结构校验（对象存在、链接类型受支持、成环检测）；
3. 计算**精确**受影响集合并逐一重算条目（`FailHook` 可注入下游更新失败）；
4. 任一条目更新失败 → 丢弃候选快照，旧快照保持可见，整体回滚；
5. 全部成功 → 指针交换发布，恰一个快照可见。

读操作用 `RLock` 取同一快照构造 `Snapshot`，一个 `Snapshot` 上的
`Entry/Lookup/AllEntries` 彼此一致，且等价于在与所有变更次序一致的某个
时间点发生的读。因此三者并发（源写入 / 链删 / 链增）天然串行等价：
测试 `TestThreeWayConcurrency` 穷举 6 种串行排列，用独立模型计算所有
可能终态，并验证并发结果必居其一；同时有并发读者检查永不出现非法中间态。

### 2.3 精确受影响集合：反向值流 BFS

把“属性 p 在实例 id 处变化”建模为值流节点 `(id, p)`。对每个以 `p` 为
`SourceProperty` 的声明 d，沿 `d.LinkType` 的**入邻接**找到所有指向 id 的
上游实例，其 d 条目受影响，然后以 `(上游, d.Name)` 继续向上传播。

- 每个 `(id, prop)` 有 visited 标记：多路径汇入同一实例时只入队一次，
  因此“一次写入触及的下游条目数”恰好等于真实、去重后的依赖数，不多不少，
  同一实例不会被处理两次。
- 链接增删的受影响集 = 属主实例在该链接类型上的条目作为种子，复用同一
  BFS 向多级上游传播。
- 删除实例以其全部基属性 + 自身派生条目为种子；删除下游时从受影响集合
  移除它自身，因此同指一个源的其他下游不受影响。

### 2.4 循环检测：在链接生效之前拒绝

声明层允许自引用声明名（如 M 的索引 `d` 经 `next` 链接读 M 上的 `d`，
这是多级传递的正常写法）；**循环是实例链接问题**。`createsCycle` 把解析
过程建模为状态 `(实例, 声明)`，对“假设已加入的边”做可达性搜索：若穿过
新边后能再次回到 `(from, 声明)` 且该声明要遍历的链接类型正是新边类型，
则新边会在传递解析中被重复穿过，判定成环，在该链接对任何查询可见之前
拒绝（`KindCycleDetected`）。既有快照在该不变量下保持无环，故任何新环
必经过新边，检测完备。

### 2.5 错误分类与固定优先级

`SourceNotFound > UnsupportedLinkType > NotUnique > CycleDetected >
DownstreamUpdateFailed`。结构校验阶段收集同时成立的条件并由
`highestPriorityError` 按固定顺序取一个；下游更新失败只可能发生在全部
结构校验通过之后，天然为最低优先级。`NOT_UNIQUE` 既是条目状态，也可经
条目查询观察；在需要作为错误报告的场景映射为 `KindNotUnique`。

### 2.6 审计日志

`Logger` 每个处理单元收到一条 `ChangeRecord`：操作输入、精确受影响下游
集合（已排序去重）、判定依据（Basis）、每个受影响条目的结果状态，以及
回滚时的错误类别。`TestRandomDifferential` 用文本 logger 实际打印这些
内容（`go test -v` 可见）。

## 3. 被放弃的方案

- **增量物化 + 每链接触发器**：易在“源写入 + 链删 + 链增”交织时漏掉
  某次更新，且证明“恰好 N 个、无重复”需要额外去重簿记；放弃。
- **声明层禁止一切自引用**：会误杀“同类型多级传递”（如链式 next 指针）
  这一需求明确要求支持的场景；改为实例链接成环检测。
- **多版本时间戳 / 细粒度锁**：能换来更大写并行度，但本任务的正确性
  焦点是串行等价与精确影响集；单写者 COW 使证明最短、测试最强，后续可
  在快照内分区加锁优化，不改变外部语义。
- **删除源后保留悬空链接以产出 SOURCE_GONE**：规范要求同单元内转为不可
  索引；直接在同单元拆掉关联链接并产出 `NO_LINK` 更简单，且与朴素模型
  完全一致。`SOURCE_GONE` 枚举保留用于防御性解析。

## 4. 目录与 API 摘要

- `derived/store.go`：快照状态、深拷贝、邻接维护。
- `derived/engine.go`：有效值解析、条目重算、反向 BFS、成环检测。
- `derived/mutations.go`：AddObject / AddDeclaration / SetProperty /
  AddLink / DeleteLink / DeleteObject（均为处理单元）。
- `derived/query.go`：`Snapshot`、`Entry`、`Lookup`、`AllEntries`。
- `derived/errors.go`：错误类别与固定优先级。
- `derived/naive.go`：独立朴素重算模型（只存原始对象与链接三元组，
  不使用任何邻接索引）。
- 测试：`derived_test.go`（单层、唯一性即时翻转、回滚、多级、级联删除、
  优先级）、`concurrency_test.go`（三者并发串行等价、精确受影响数量证明）、
  `diff_test.go`（6000+ 随机操作对拍、注入失败回滚对拍、审计日志）。

## 5. 本地验证

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath   # 仅当 HOME 缓存目录只读时需要

go test ./...
go test -race -v ./derived/
go test -run TestRandomDifferential -v ./derived/   # 查看逐条审计日志
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
gofmt -l . && go vet ./...
```

实测：全部用例在 `-race` 下通过，包语句覆盖率约 90%。
