# 跨实例联合版本前置条件批量更新 —— 设计说明

## 1. 语义定义

一个 `Batch` 由两部分组成：

- `Preconditions`：若干 `(实例, 期望版本)` 联合前置条件；
- `Ops`：若干变更（`set_attr` / `add_link` / `remove_link`）。

提交一个批次的结果**有且仅有**四种，互斥且按固定顺序判定：

1. `duplicate_precondition`：批次内对同一实例声明了多条前置条件（在读取任何实例
   版本之前判定，因此它不需要也不可能受并发影响）；
2. `version_mismatch`：在同一个逻辑时刻读取所有涉及实例的当前版本后，至少一项
   与声明不符（实例不存在同样归入此类，携带 `missing: true` 证据）；
3. `cardinality_violation`：版本条件全部满足后，在“假设生效后的影子状态”上校验
   链接基数（含未知链接类型、端点类型不匹配等 schema 违规），至少一处越界；
4. `committed`：整批原子生效。

关键不变量：

- 判定在前、修改在后。所有变更先写入深拷贝的影子状态，只有三关全过才发布到活
  状态；不存在“先改一部分再回滚”的路径。被拒绝批次在外部可观察状态上与从未发
  生完全不可区分（仅日志中留下判定记录）。
- 每个实例维护自己的单调版本序列（`version += step`，默认 `step = 1`）。联合批
  次不统一、不绑定各实例的版本号；一个实例连续单独推进到 5，另一个仍可以是 1，
  之后两者出现在同一批次时各自只前进一格。

## 2. 并发控制：全局排序的严格两阶段锁

### 2.1 方案

- 每个实例有一把互斥锁；另有一把只保护实例注册表（map）的短锁。
- 批次计算自己触及的全部实例（前置条件实例 + 所有 Op 的实例及链接对端），按
  ID 升序排列，**一次性按序获取全部实例锁**，判定 + 变更 + 发布全部完成后再
  统一逆序释放（strict 2PL）。
- 固定全序加锁使等待图无环，不会死锁。

由此直接得到：

- **联合读取的原子性**：读取所有版本时所有相关锁都在本批次手中，其他批次不可
  能在“读取之后、判定完成之前”改动其中任何实例。
- **同版本争夺的恰好一方获胜**：两个实例集合有交集的批次必然在交集锁上串行
  化。先获锁者读到旧版本并提交、把版本推进一格；后获锁者读到新版本，其前置条
  件（期望旧版本）必然失败而被整体拒绝。两者不可能基于同一版本同时生效。
- **可串行化**：交集即冲突，冲突即按锁获取顺序串行，无交集的批次 commute。因
  此任意交错历史等价于按“锁获取顺序”串行执行的历史。

### 2.2 线性化点与可重放证据

批次在“全部锁获取完成”的瞬间获得一个单调递增的 `acquire_order`（单个全局计数
 器，仅在加锁路径触碰，O(1)）。这就是该判定的线性化点。日志按决策顺序记录：

```
批次完整输入（前置条件 + Op）
  → 联合判定时实际读到的每个版本 observed
  → 分类结果及明细（mismatch / cardinality / versions from→to）
  → order 与 acquire_order
```

`cmd/replay` 读取日志、按记录顺序重建状态并重跑每个批次，逐条比对重放结果与原
始证据。因为记录顺序就是线性化顺序，重放必然重现同一结果（这本身是对并发实现
正确性的独立交叉验证）。

### 2.3 判定开销不随实例总数增长

联合版本判定只做 `k` 次 map 直查（`k` = 批次前置条件数），锁集合只解析批次触及
的实例；整段代码从不遍历全量实例表。基数校验只检查本批次实际改动的
`(实例, 链接类型, 端点)` 三元组。基准数据（本机，拒绝路径做的判定工作与成功路
径完全相同）：

| 总实例数 | ns/op（k=4 固定） |
| --- | --- |
| 1,000 | ~1344 |
| 16,000 | ~1158 |
| 64,000 | ~1915 |

随批次规模 k=1/4/16/64 则线性增长（约 0.9µs → 31µs）。复现：

```bash
go test -run xxx -bench BenchmarkCommitVersionGate -benchtime=200x ./ontology/
```

“不依赖额外对外暴露状态的证据”即：判定只依赖实例自带的版本号；`acquire_order`
只写入日志/结果作为证据，不参与判定逻辑。

## 3. 代码结构

- `ontology/types.go`：类型定义（实例、链接类型、批次、Op、四类结果、证据结构）。
- `ontology/store.go`：Store、实例锁、注册表、快照读取。
- `ontology/batch.go`：`Commit` 主流程：排序加锁 → 三关判定 → 提交 → 记录。
- `ontology/gates.go`：影子状态暂存、增量基数校验、深拷贝/发布。
- `ontology/journal.go`：追加式判定日志与 NDJSON 导出。
- `ontology/replay.go`：日志重放与证据逐条比对。
- `naive/naive.go`：**独立**参考实现——自有数据结构，一把全局锁串行执行
  `Commit`，与生产代码零共享逻辑。
- `cmd/demo`：生成样例日志；`cmd/replay`：重放校验日志。

## 4. 被放弃 / 考虑过的方案

1. **乐观并发（读版本 → 校验 → CAS 写）+ 冲突重试**：放弃。题目明确禁止“先修改
   部分实例再发现冲突”的外部可观察效果；即使事务回滚，朴素 CAS 也需要多对象原
   子提交协议，且重试会让“恰好一方读到真实版本”的判定证据依赖内部重试次数，不
   利于重放核验。
2. **全局一把锁串行化所有批次**：语义最直接、实现即 `naive` 模型，但任何两个不
   相交批次也被串行化，且联合判定开销/竞争与全局对象数相关。它保留为**正确性
   预言机**，生产路径采用按实例排序加锁，只串行真正冲突的批次。
3. **多版本（MVCC）+ 时间戳排序**：读快照天然原子，但要引入全局时间戳/TSO 与
   垃圾回收；本需求只需要“当前真实版本”的联合判定，MVCC 的历史版本是多余负担，
   全局 TSO 也重新引入了单点。
4. **两阶段提交（2PC）协调器**：跨实例分布式语义的标准答案，但本系统是单进程内
   存状态，2PC 的 prepare/commit 日志与协调失败恢复属于过度设计；排序加锁已在本
   模型内提供相同的可串行化与原子可见性。
5. **基数全量扫描**：放弃。提交后扫描所有实例所有链接类型的度会让开销随系统总
   规模增长；改为只校验本批次改动过的度三元组。

## 5. 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache   # 按本机环境
go test -race ./...                                         # 全量 + 竞态
go test -race -count=5 ./naive/                             # 差分 + 并发重放
go test -run xxx -bench . -benchtime=200x ./ontology/       # 开销基准
go vet ./... && gofmt -l .

go run ./cmd/demo -out /tmp/decisions.ndjson
go run ./cmd/replay -journal /tmp/decisions.ndjson
```

测试覆盖：

- `TestCompetingBatchesExactlyOneWins`：用测试钩子制造确定性的“A 持锁、B 等
  待”交错，断言 A 提交、B 以 `(expected=1, actual=2)` 被整体拒绝且未触碰第三
  方实例；
- `TestManyConcurrentSameVersionContest`：30 轮、每轮 6 个竞争者对同一前置版本
  发起联合批次，断言每实例每轮恰好 1 个提交、版本严格 +1、属性为最终赢家所写、
  日志 order 无重复；
- `TestDuplicatePreconditionCheckedFirst` / `TestVersionMismatchRejectsWholeBatch`
  / `TestCardinalityExactlyAtBoundary`（MaxSource=2，第三条约会触发）
  / `TestRejectedBatchLeavesNoTrace` / `TestIndependentVersionProgressions`；
- `naive/diff_test.go`：
  - `TestSerialDifferential`：40 个随机种子 × 120 个随机批次，生产实现与朴素
    全局锁模型逐条结果与最终状态一致；
  - `TestConcurrentReplayEquivalence`：21 个种子 × 200 个批次并发执行，按
    `acquire_order`（线性化顺序）喂给独立朴素模型重放，断言每个批次成败一致、
    每次提交的 `from→to` 版本变化一致、最终版本与属性状态一致；随后再用
    `ReplayJournal` 对生产日志独立重放并核对。
