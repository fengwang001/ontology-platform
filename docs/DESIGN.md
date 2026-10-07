# 对象类型版本迁移与运行中实例双写回填子系统 — 设计说明

## 总览

子系统由三个协作模块组成,外加一个仅用于对照测试的朴素参考模型:

| 模块 | 包 | 职责 |
| --- | --- | --- |
| 版本声明与兼容性判定 | `ontology/migration` | 迁移声明的表示、矛盾校验、生效追踪、新旧视图现算 |
| 读写路由与一致性仲裁 | `ontology/router` | 实例存储、新旧版本读写路由、删除、声明变更、全局串行化仲裁、两阶段回填钩子 |
| 存量实例异步回填 | `ontology/backfill` | 按确定性内部顺序调度回填,报告应用/跳过结果 |
| 朴素参考模型(测试用) | `ontology/naive` | 保留全部历史对应关系、每次读取重放重算,作为正确性基准 |

## 核心不变式

1. **新版本读取结果与回填进度无关**:对同一实例,无论它是否已被物理回填,
   新版本读到的视图完全相同(回填前即时现算,回填后直接读存储)。
2. **任何写入都是一次回填**:新旧版本的写入都会把实例转换为新版本结构,
   旧版本写入先作用于旧视图、再按迁移声明转换存储,因此旧路径不会绕开
   新结构的约束与默认值规则;写入完成后实例视为已回填。
3. **回填是一次特殊写入**:与正常读写、删除、声明变更在同一把仲裁锁下
   串行化;被并发写入抢先或实例已删除时识别并跳过,不覆盖、不复活、不报错。
4. **追加的对应关系只影响未回填实例**:已回填实例不会被二次回填。

## 关键取舍

### 合成映射 vs 重放历史(性能要求的来源)

迁移声明支持在回填进行中反复追加对应关系。若每次现算视图都重放全部
历史变更,开销会随历史变更次数线性增长,违反「现算开销只与实例自身
属性数量相关」的要求。因此 `migration.Declaration` 在每次 `Amend` 时把
全部历史折叠为一份平坦的**合成映射**(旧属性规则表 + 新增属性默认值表),
视图现算只扫描实例自身属性并应用默认值:
`ForwardViewStats` 返回每次现算的操作数,
`migration.TestForwardViewCostIndependentOfAmendments` 验证追加 2000 次
历史变更后操作数与基线完全一致;`naive` 模型则保留全部历史、每次读取
重放,`difftest.TestNaiveCostGrowsWithHistory` 展示其代价随历史增长,
两者形成对照证明。

### 全局互斥锁仲裁 vs 分片锁

`router.Service` 用一把 `sync.Mutex` 串行化所有操作。这直接满足
「最终效果等价于某个全局串行顺序」与「重放结果完全一致」的要求,
且让「参数非法在前、实例不存在次之」的拒绝次序易于推理。
代价是单对象类型的吞吐上限;若未来需要扩展,自然的演进方向是按实例
ID 分片加锁 + 声明级读写锁,但回填与声明变更之间的排序需要额外的
代际协议,复杂度显著上升,当前规模下不值得。

### 两阶段回填 + 代际号 CAS

回填分为 `PrepareBackfill`(锁内快照实例数据与代际号,现算新数据)与
`Apply`(重新加锁,校验代际号后替换)。这精确对应「回填即将应用时被
正常写入抢先」的竞争窗口:

- 代际号不一致(被写入抢先、或删除后重建)→ `BackfillSkippedStale`,不覆盖;
- 实例不存在 → `BackfillSkippedDeleted`,不复活;
- 两种跳过都不是错误。

被放弃的方案:回填在 Prepare 时把实例标记为「回填中」并阻塞并发写入——
会让回填故障传导为写入不可用,且需要处理标记泄漏,复杂度更高。

### 生效追踪的粒度

「不允许修改已生效的对应关系」按**属性级**追踪:一次回填应用后,
新增属性的默认值总是记为生效,旧属性的保留/废弃规则只在该属性真实
出现于被回填的旧数据中时才记为生效。被放弃的更保守方案(首次回填后
全部对应关系锁定)会不必要地拒绝合法变更;更宽松的方案(不追踪)
则无法兑现「已回填实例的视图不可被声明变更 retroactively 改变」。

### 未声明的旧属性默认保留

迁移声明只需描述变化:未出现在声明中的旧属性在两个版本间默认保留。
显式 `retain` 声明主要用于文档化意图与矛盾检测(同一属性同时声明
保留与废弃会被拒绝)。

### 废弃属性不可写,但可随旧实例创建

废弃属性即将被移除:两个版本的**写入**都拒绝它(允许写入只会被转换
静默丢弃,拒绝比丢数据诚实);但以旧版本结构**创建**实例时允许携带,
因为那正是待迁移的历史数据。

## 错误模型与拒绝次序

所有拒绝路径先校验参数合法性、再检查实例存在性,只报告第一个命中的原因:

- `router.ErrInvalidArgument`:声明自相矛盾、修改已生效对应关系、
  写入该版本下不可写的属性、版本号非法等;
- `router.ErrNotFound`:目标实例不存在。

被拒绝的操作不改变实例的回填状态与任何版本下的可见数据
(`router.TestRejectedOpsChangeNothing`)。`Amend` 采用「全部校验通过
才统一应用」的两阶段提交,拒绝时不留部分变更。

## 确定性

- 回填的内部顺序为实例 ID 字典序(`PendingIDs`);
- 所有操作经同一仲裁锁串行化;
- `difftest.TestReplayDeterminism` 用同一种子重放全部操作与回填触发
  序列两次,逐字节比对每步结果,验证完全确定性。

## 被放弃的方案汇总

- **读时重放全部历史对应关系**:违反现算开销要求,见上。
- **实例双份存储(新旧各一份)+ 双写同步**:两份副本本身就是一致性
  隐患,任何仲裁漏洞都会直接表现为新旧视图矛盾;单副本 + 视图现算
  在结构上排除了这种可能。
- **回填加写阻塞标记**:见「两阶段回填」。
- **按实例分片锁**:见「全局互斥锁仲裁」。

## 本地验证方法

```bash
# 全量测试(含竞态检测)
go test -race ./...

# 查看对照测试打印的输入/实际输出/判定依据
go test -v -run TestDifferentialRandomSequences ./ontology/difftest

# 现算开销与历史变更次数无关的证明
go test -v -run TestForwardViewCostIndependentOfAmendments ./ontology/migration
go test -v -run TestNaiveCostGrowsWithHistory ./ontology/difftest

# 竞争专项:回填 vs 写入(跳过而非覆盖)、回填 vs 删除(跳过而非复活)
go test -v -run 'TestBackfillSkips' ./ontology/router

# 追加对应关系只影响未回填实例
go test -v -run TestAmendAffectsOnlyPendingInstances ./ontology/router

# 演示程序
go run ./cmd/server

# 静态检查
gofmt -l . && go vet ./...
```

## 测试覆盖地图

| 需求 | 测试 |
| --- | --- |
| 回填 vs 并发写入:跳过而非覆盖 | `router.TestBackfillSkipsWhenWriteWins`、`backfill.TestWorkerSkipsConcurrentlyWrittenInstances` |
| 回填 vs 删除:跳过而非复活 | `router.TestBackfillSkipsWhenInstanceDeleted` |
| 追加对应关系只影响未回填实例 | `router.TestAmendAffectsOnlyPendingInstances` |
| 修改已生效对应关系被拒绝 | `router.TestAmendRejectsModifyingEffectiveMapping`、`migration.TestAmendRejectsModifyingEffectiveMapping` |
| 声明自相矛盾被拒绝 | `migration.TestAmendRejectsContradiction*` |
| 旧写入等价转换为新写入 | `router.TestOldVersionWriteIsConvertedToNewVersionWrite`、`TestOldAndNewWritesConverge` |
| 新版本读未回填实例即时现算 | `router.TestNewVersionReadComputesViewOnTheFly` |
| 拒绝次序与错误可区分 | `router.TestRejectionOrderInvalidBeforeNotFound` |
| 被拒绝操作不改变状态 | `router.TestRejectedOpsChangeNothing` |
| 并发串行化等价 | `router.TestConcurrentReadWriteBackfill`(`-race`) |
| 重放确定性 | `difftest.TestReplayDeterminism` |
| 与朴素模型随机对照 | `difftest.TestDifferentialRandomSequences`(5 个种子 × 600 操作,逐步打印输入/输出/依据) |
| 现算开销与历史变更次数无关 | `migration.TestForwardViewCostIndependentOfAmendments` vs `difftest.TestNaiveCostGrowsWithHistory` |
