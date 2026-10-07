# 子图快照提取器 — 设计说明

## 1. 目标与范围

在通用本体模型（对象类型、含方向的链接类型、基于存在性的权限模型）之上，实现
`Graph.Extract(caller, scope)`：给定一个范围对象标识集合，从主图中抽取某一确定时点
（epoch）的自洽子图快照，并在附属信息中报告范围边界处被排除的悬挂链接。

代码全部位于 `ontology/` 包，零第三方依赖。

## 2. 数据模型与关键取舍

### 2.1 主图与版本化

- `Graph` 内部以 map 保存对象类型、链接类型、对象、链接；额外维护
  `adjacency: ObjectID -> set<linkKey>` 邻接索引。这是“提取开销不随主图整体规模
  增长”的结构性保证：提取只沿范围内对象的邻接集合走，从不扫描全图 map。
- 每个成功的写操作令单调递增的 `Epoch` 加 1，并向只增日志 `Journal` 追加一条或多条
  `Change`（删除对象时级联删除的链接与对象删除共用同一个新 epoch）。读操作不推进
  epoch。Epoch 即“确定时点”。
- 链接用 `linkKey{type, a, b}` 规范化存储：无向链接两端按 id 排序，`{A,B}` 与
  `{B,A}` 是同一条链接；有向链接保持方向。自环两端相同，邻接集合只登记一次。

### 2.2 权限模型

- 每个对象携带 `Readers []Principal`：调用者出现在 Readers 中才拥有“存在性权限”，
  即可以知道该对象存在并看到其内容。
- 无存在性权限的对象对调用者视同不存在：快照不包含它，且其 id 不得通过附属信息泄露。
  因此悬挂记录中，若调用者对远端对象无存在性权限，远端 id 以 `RemoteRedacted=true`
  脱敏（`RemoteEnd` 置空）。`Snapshot.removed`（被剔除对象集合）刻意不导出，避免
  成为存在性侧信道。

### 2.3 提取的严格判定次序

1. **参数非法**（在获取任何图状态之前判定）：
   - scope 为空（nil 或长度 0）；
   - 任一标识格式非法（空串、含空格或 ASCII 控制字符、超过 512 rune）；
   - 去重后范围大小超过 `MaxScopeSize = 10_000`。
   - 重复 id 按“集合”语义静默去重。
2. **剔除后为空**：权限剔除先于悬挂判定；剔除后（以及当前时点不存在的范围 id 忽略后）
   没有任何可见对象时，返回 epoch 固定、三个字段均为空切片的快照，**不是错误**。
3. **正常提取**。

### 2.4 悬挂链接的分类与优先级

对每条“恰好一端被纳入快照”的已检查链接，记录一条悬挂信息：

- `DanglingByScope`（范围边界）：远端对象从未出现在请求 scope 中；
- `DanglingByPermission`（权限剔除）：远端对象原本在 scope 中，但因调用者无存在性
  权限被剔除。

判定严格按 **范围边界优先于权限剔除**：只要远端不在请求集合中，一律标注
`DanglingByScope`（即使调用者同时也无权看到它——这正是两种条件同时满足的情形）。
只有“远端在请求集合中且被剔除”才标注 `DanglingByPermission`。

补充规则：

- 两端都未纳入的链接（例如 scope 内两个对象都被剔除）完全不出现在任何输出中；
- 自环两端是同一对象：纳入即为内部链接，剔除即整体消失，**永不进入悬挂判定**；
- scope 中包含“当前时点不存在”的 id（从未创建或已删除）不构成参数非法，也不产生
  任何记录——在该时点它不是对象；级联删除保证不存在指向已删对象的链接。

### 2.5 时点一致性与并发可串行化

- 提取全程持有一把 `sync.RWMutex` 的读锁；epoch 读取、权限判定、邻接遍历、镜像复制
  全部在同一把锁的同一临界区内完成。写操作持写锁并先推进 epoch 再发布新状态。
  Go 的 RWMutex 语义保证：写锁待入后新读锁排队，因此所有 Extract 与所有变更的某个
  实际交错，等价于把每个操作排成某个全序串行序列；一次 Extract 内部看到的所有对象与
  链接状态对应该序列中**唯一确定的一个 epoch**，不可能把不同时点的状态拼在一起。
- 这是用“粗粒度锁 + 不可变结果复制”换取可证明的简单正确。快照对象在锁内做防御性
  拷贝（含 Readers 切片），返回后与主图完全解耦，后续变更不可能改写旧快照。

### 2.6 确定性输出

对象按 id 排序；内部链接按 `(type, source, sink)` 排序；悬挂记录按
`(localEnd, type, source, remoteEnd, direction)` 排序。排序键只含可见字段，因此
脱敏记录之间的顺序同样确定。同一 scope、同一调用者、同一 epoch 的两次提取结果
`reflect.DeepEqual` 完全一致（含悬挂信息的内容与顺序）。

### 2.7 内部可验证的开销度量

`extractMetrics`（不导出，不进入任何对调用者可见的接口）记录：

- `scopeMembershipChecks`：去重后范围大小；
- `permissionChecks`：对范围对象的存在性权限判定次数；
- `candidateLinksExamined`：本次提取**实际通过邻接索引触达并检查过的不同链接数**。

只统计实际检查的候选链接；被去重的邻接键不重复计数。该值只随范围规模与范围内对象的
直接关联链接规模增长，与主图其他区域无关。包内测试（
`TestExtractionCostIndependentOfUnrelatedGraphSize`）加入 2000 个对象 / 4000 条
互不相连的链接后，同一提取的候选数保持不变；范围邻域每增加一条边，候选数恰好增加 1。

## 3. 被放弃的方案

1. **MVCC / 写时复制（每次写操作复制全图）**：能提供无锁读与历史版本，但全图复制
   成本随主图规模增长，直接违背“提取开销不随无关规模增长”；且 Go 中实现增量 COW
   （per-node 版本指针 + GC）复杂度高、易错。放弃，改用 RWMutex 单临界区：读多写少
   场景下性能足够，正确性可局部证明。
2. **提取时逐对象二次查询主图（无邻接索引）**：实现最简单，但每次都要扫全链接表，
   开销随主图规模线性增长，不满足要求。
3. **悬挂信息中始终暴露远端 id**：会成为存在性侧信道（调用者可借此枚举无权对象）。
   放弃，改为按存在性权限脱敏；代价是同一时刻不同调用者看到的附属信息不同，这与
   权限模型一致且必需。
4. **“剔除后为空”返回错误**：需求明确为空快照非错误；且错误与空结果会让调用方无法
   区分“参数问题”与“合法但无可见内容”，故采用三态判定次序。
5. **以时间戳作为时点**：墙钟在并发下不单调、不可复现。改用逻辑 epoch + 变更日志，
   差异归因可以精确到“窗口内具体哪些 Change”。

## 4. 差异可归因性

两次同参提取：

- 若 `Epoch` 相同，则结果逐字段一致（确定性输出保证）；
- 若 `Epoch` 不同，则两次 epoch 之间的所有 `Change` 可从 `Journal` 枚举，快照差异
   只能涉及这些变更触及的对象/链接。测试 `TestExtractRepeatabilityAndAttribution`
   对窗口内日志条目逐条核对。

## 5. 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache   # 若默认缓存目录只读

go test ./...                  # 全量测试
go test -race -count=3 ./...   # 竞态 + 重复
go test -run TestRandomDifferentialAgainstNaiveOracle -v ./ontology
gofmt -l .
go vet ./...
```

随机差分测试每次运行把**每次提取的输入（seed/step/caller/scope）、输出（epoch/
objects/links）、悬挂附属信息及朴素模型对照结果**写入测试临时目录下的
`extraction-log.jsonl`（JSON Lines，路径在 `-v` 输出中打印）。

## 6. 测试覆盖对照

| 需求点 | 测试 |
| --- | --- |
| 参数非法先于一切 | `TestExtractInvalidArguments` |
| 剔除至空返回空快照非错误 | `TestExtractStrippedToEmpty` |
| 范围边界 vs 权限剔除来源区分、脱敏、优先级 | `TestExtractDanglingSources` |
| 自环不计悬挂 | `TestExtractSelfLoop` |
| 重复提取一致性 + 变更可归因 | `TestExtractRepeatabilityAndAttribution` |
| 朴素穷举模型随机对照 + 记录 | `TestRandomDifferentialAgainstNaiveOracle` |
| 并发可串行化 / 内部一致性 / 同 epoch 确定性 | `TestConcurrentSerializability` |
| 开销度量与无关规模解耦 | `TestExtractionCostIndependentOfUnrelatedGraphSize` |
