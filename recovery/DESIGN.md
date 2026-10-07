# 快照损坏修复与动作重放 · 原子性协调器设计说明

包路径：`ontology/recovery`（无第三方依赖，仅标准库）。

## 1. 问题与建模

快照由多条**对象级**记录组成，每条记录自带校验信息（FNV-1a 校验值）。
动作日志记录快照时刻之后的每个动作，每条动作声明：

- 涉及的对象集合 `Effects map[ObjectID]Effect`；
- 对每个对象分别产生的效果 `Effect`：
  - `HasStart=false`：只有增量 `Change`，必须作用在一个**已知状态**上；
  - `HasStart=true`：动作自带该对象的完整起点 `Start`，
    即使对象此前状态完全未知，也能由 `(Start, Change)` 在本动作处**锚定**。

这一区分直接承载需求中“起点状态与变更”的语义：若动作只携带增量，
它无法修复一个起点完全未知的对象；只有携带完整起点的完整动作才能锚定。

对象在三个维度上被严格区分，互不混淆：

| 维度 | 取值 |
|---|---|
| 快照层面 `ObjectStatus` | `absent`（不存在）/ `valid`（完好）/ `corrupt`（损坏=快照时刻不可读） |
| 最终来源 `ObjectSource` | `snapshot`（完好快照）/ `replay`（动作重建）/ `unreadable`（仍不可读） |
| 请求判定 | 超覆盖范围 / 版本不衔接 / 正常逐对象报告 |

“不可读”与“不存在”的区别：`corrupt` 是快照中**有记录但校验失败**，
`absent` 是快照中根本没有该 key；二者来自不同的判定路径，报告分别给出。

## 2. 校验：记录即边界

- 快照逐条校验：`SnapshotChecksum(obj, state)`。单条失败只把**该对象**
  标记为 `corrupt`，不影响其它对象，也不上浮成整个快照不可用。
- 动作逐条校验：`ActionChecksum(id, base, effects)` 覆盖动作 ID、版本、
  **排序后的全部对象效果字段**。任何一个字段（包括恰好落在对象集合
  边界成员上的那个字段）被篡改，整条记录失配 → **整条丢弃**，
  代码里不存在“取其中可解析字段”的分支。

损坏不是请求被拒绝的理由：`snapshot-corrupt` 与 `action-corrupt` 是
修复结果，分别列入报告的 `CorruptObjects` 与 `CorruptActions`。
只有结构性问题（版本不衔接、请求超范围）才作为 error 返回。

## 3. 原子重放规则（核心）

有效动作序列（已剔除损坏记录）依次处理。对每条动作，**先整体预检，
再整体生效**：

1. 预检：遍历动作涉及的全部对象。若存在对象当前状态未知，
   且该动作为它携带的效果 `HasStart=false`（没有完整起点），
   则该对象是“阻塞者”，**整条动作放弃**——包括集合中本来已知的对象，
   等价于该动作从未发生。
2. 生效：预检通过后，对集合中每个对象统一应用：
   - 此前未知：用 `Start` 在此锚定（记录 `AnchorIndex`），
     之后沿后续完整动作传播；**不向更早回溯**；
   - 此前已知：以当前状态（或动作显式给出的 `Start`）为基叠加 `Change`。

被放弃的动作不消耗任何对象的状态，因此后续动作的判定不受其影响
（例：`a1` 完整、`a2` 损坏、`a3` 因原子性放弃、`a4` 锚定未知对象——
`a` 的状态只沿 `a1 → a4` 传播）。

状态变换由 `Applier` 接口抽象，默认实现为确定性拼接
`base + "|" + change`，便于独立实现同一约定做差分对照。

## 4. 拒绝优先级

纯函数入口 `Evaluate(snap, log, objectIDs)` 显式编码：

1. **对象超出覆盖范围**（最高）。覆盖范围 = 快照记录对象 ∪
   全部**完整**动作涉及对象；损坏动作的对象集合不可信，不计入。
2. **版本不衔接**：`snapshot.Version != log.Base`。
3. 均不命中 → 逐对象给出来源分类，返回报告，不是错误。

即使两种问题同时存在，也只返回超范围错误（用 `errors.Is` 区分，
不混报）。`NewCoordinator` 在构造时保证版本衔接，
运行中的 `Recover/Classify` 因而只可能命中超范围拒绝。

## 5. 并发与全局串行等价性

`Coordinator` 用一把 `sync.RWMutex` 串行化所有可观察状态变更：

- 读路径（`Recover`、`Classify`、`DecisionLog`）共享读锁，只看到
  **已发布**的完整报告，永远观察不到“部分恢复”的中间态；
- 写路径（`AppendAction`）持写锁，先在**临时副本**上校验与重放，
  全部成功后才在锁内一次性提交报告（copy-on-write 风格）。
  追加损坏动作在提交前返回 `ErrActionCorrupt`，不改变快照、日志或
  任何既有判定。

因此“修复进行中到达的新动作”要么完整排在修复前、要么完整排在
修复后，可观察历史等价于某个全局串行顺序。增量追加的最终结果与
一次性批处理一致（`TestRandomIncrementalMatchesBatch`，200 组随机用例）。

## 6. 规模无关的来源定位性能（可验证）

- 重放在构造/追加时一次性完成，结果物化为对象级报告
  `map[ObjectID]ObjectReport`，并维护 `AnchorIndex`（最早锚定动作下标）。
- `Classify(obj)` 只做一次 **map 查找，O(1)**，不遍历任何动作记录，
  与日志总长度无关；`Recover(ids)` 为 O(|ids|)。
- 扫描日志的 O(L) 成本只发生在写入侧（追加/构造），属于重放本身
  无法避免的工作量，不在“定位来源分类”的路径上。

可验证方式：

- 代码层面：`Classify` 函数体内无循环、无日志访问，直接索引报告 map；
- 测试层面：`TestClassifyConstantTime` 在 64 与 8192 条日志两个规模下
  验证语义一致、且锚点通过下标一次命中（`AnchorIndex==8192`），
  定位路径不随 L 增长；
- 若需要更强的机器可核证据，可在该用例上加 benchmark
  `BenchmarkClassify`，对比不同 L 下的 ns/op 为常数（本次未纳入默认测试）。

## 7. 错误类别（可区分、不混报）

| 哨兵错误 | 触发 | 载体 |
|---|---|---|
| `ErrSnapshotCorrupt` | 快照对象级记录损坏 | 报告 `CorruptObjects` / `SnapshotCorruptObjects()` |
| `ErrActionCorrupt` | 动作记录损坏（整条不可用） | 报告 `CorruptActions` / `ActionCorruptIDs()` / 追加返回值 |
| `ErrVersionMismatch` | 日志起点与快照版本不衔接 | `error`（`errors.Is`） |
| `ErrObjectOutOfCoverage` | 请求对象不在覆盖范围 | `*CoverageError`（含对象清单），优先级最高 |

## 8. 被放弃的方案与关键取舍

- **放弃“尽力采信损坏动作中的可解析字段”**：违反原子性且无法界定
  可信边界。选择记录级校验 + 整条丢弃，简单且可审计。
- **放弃把损坏上浮为整体修复失败**：需求要求对象级粒度与逐对象报告，
  因此损坏只是结果标签，请求仍正常返回。
- **放弃读取时按需重放**：会让 `Classify` 变成 O(L)。选择写入时物化、
  读取 O(1)，用写侧成本换取规模无关的查询定位。
- **放弃多版本乐观并发（MVCC）**：本场景读写比虽高，但一把读写锁 +
  copy-on-write 已能给出可证明的串行等价性且实现最小化；高争用可再
  演进为原子指针发布不可变报告，语义不变。
- **起点状态建模为显式 `HasStart` 标志**，而不是约定“空串即起点”：
  空串是合法状态值，用标志位避免歧义，也让“只给增量”的语义显式化。
- **不向锚点之前回溯**：锚点之前的状态判定保持 `unreadable`，
  避免用后续动作内容臆造历史。

## 9. 本地验证方法

```bash
# 环境：Go 1.26.5；若 GOCACHE 默认目录只读，可指向 /tmp
export GOCACHE=/tmp/gocache

go test ./...                       # 全量（含 700 组随机差分/增量用例）
go test -race -v ./...              # 竞态检测 + 判定日志（输入/输出/依据）
go test -cover ./...                # 覆盖率（当前 94.6%）
go vet ./... && gofmt -l .          # 静态检查与格式
```

测试覆盖与需求条目的对应：

- 损坏恰好落在动作对象集合边界：`TestCorruptionOnActionSetBoundary`；
- 部分可读/不可读时原子性整体放弃：`TestAtomicityMixedReadability`；
- 连续动作夹损坏记录的传播：`TestConsecutiveActionsWithCorruptInBetween`、
  `TestActionCorruptWholeRecordAndPropagation`；
- 随机损坏模式 × 动作序列对照朴素模型：
  `TestRandomDifferentialAgainstNaive`（500 组）；
- 并发串行等价 + 拒绝无副作用：`TestConcurrentSerializable`、
  `TestAppendCorruptActionNoSideEffect`；
- 拒绝优先级与四类错误区分：`TestRejectPriority`、
  `TestErrorCategoriesDistinct`；
- 规模无关定位：`TestClassifyConstantTime`。

判定日志通过 `DecisionLog()` 暴露，`-v` 运行时打印每条快照校验、
动作校验、版本判定、动作应用/整体放弃、对象分类的输入、输出与依据。

## 10. 文件清单

| 文件 | 职责 |
|---|---|
| `types.go` | 领域类型、来源/状态枚举、四类哨兵错误 |
| `checksum.go` | 快照/动作记录校验值（FNV-1a，顺序无关） |
| `load.go` | 逐条校验与视图构建（损坏隔离/整条丢弃） |
| `replay.go` | 原子重放引擎、锚点索引、判定日志 |
| `coordinator.go` | 并发协调器、请求入口、copy-on-write 发布 |
| `evaluate.go` | 纯函数请求入口与拒绝优先级 |
| `naive.go` | 独立朴素重放参照模型（差分测试预言机） |
| `*_test.go` | 功能、边界、并发、随机差分与增量一致性测试 |
