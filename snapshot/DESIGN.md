# 分块快照完整性校验与跨类型聚合导出器 — 设计说明

## 1. 目标与范围

本体平台的一次快照导出按**对象类型**拆分为多个块文件落盘，块间通过跨类型
链接字段互相引用。加载侧必须在**不信任任何块**的前提下，独立判定每个块的
可信性，并对跨块引用给出**确定、可重复、保守而不武断**的结论。

代码位于 `snapshot/` 包，四个职责：

- `exporter.go`：按类型分块、计算校验和、写清单与块文件；
- `verify.go` / `loader.go`：只读校验流水线与加载/聚合入口；
- `index.go`：带探测计数的 O(1) 引用索引；
- `naive.go`：**独立**朴素对照模型（供差分测试，非生产路径）。

## 2. 磁盘格式与关键取舍

导出目录：

```
manifest.json                 # 覆盖哪些类型、各类型对应哪些块文件
chunk-<Type>-<idx>.json       # 每类型可有多个块
```

块文件 `Envelope`：

```json
{
  "header": {"type": "Person", "chunk_index": 0,
             "declared_count": 2, "checksum_algo": "sha256"},
  "records": [ {"id": "p1", "data": "...",
                "refs": [{"field": "worksFor",
                          "target_type": "Company", "target_id": "c1"}]} ],
  "checksum": "<sha256(json(records)) 的 hex>"
}
```

### 取舍 A：校验和只覆盖记录，不覆盖块头

`checksum = sha256(json(records))`。块头（含 `declared_count`）与 checksum
字段本身都不纳入校验。这样“内容被篡改（校验失败）”与“条数声明不符”是
**两类互斥、可区分**的问题：

- 重算 checksum 不匹配 ⇒ `integrity_failure`；
- checksum 匹配但 `len(records) != declared_count` ⇒ `count_mismatch`。

被放弃的方案：把块头也纳入 checksum。该方案会让“改条数”退化成
“校验失败”，无法表达“内容可信但数量无法确定”这一更精细的语义，也无法
满足题目要求的五类错误相互可区分。

### 取舍 B：manifest 只声明范围，不参与块可信判定

manifest 不携带块级校验信息。每个块的可信性**完全**由自身文件决定，与其它
块、与 manifest 都无关——对应“校验结果只针对块内容本身，与其他块无关”。
manifest 不可读时只能得出“范围未知”，作为最高保守度的加载失败报告。

### 取舍 C：数量不一致 = 整块记录一律不可信

条数不符无法区分“截断”还是“多算”，因此即使每条记录单独看都完好，整块也
标记为 `count_mismatch`，`ChunkReport.Records` 置空，上层拿不到任何一条
“半可信”记录。测试 `TestCountMismatchWholeChunkUntrusted` 同时覆盖
“声明偏大”和“截断后重算 checksum”两种构造。

## 3. 校验流水线（Load / Aggregate 共用）

1. 读 manifest，确定覆盖范围；请求中不在范围的类型记 `out_of_range`。
2. 对 manifest 中的**全部**块独立做完整性校验与条数核对，结论互不影响。
   非请求类型的块也照常校验（结果在 `Reports` 里可见，供诊断），但不作为
   本次请求的“命中项”进入 `Issues`。
3. 仅对“请求且在范围内”的类型，检查其**可信块**内的全部跨类型引用。

### 引用判定的保守三分支（不得互换）

| 目标情况 | 结论 |
| --- | --- |
| 目标类型不在导出覆盖范围 | `reference_unverifiable` |
| 目标类型存在，但任一块 integrity/count 不可信 | `reference_unverifiable` |
| 目标类型所有块都可信，索引未命中 | `dangling_reference` |
| 目标类型所有块都可信，索引命中 | resolved（不报问题） |

关键约束：目标块不可信时**不得**默认悬空、也**不得**默认有效，只能报告
“因目标块不可信而无法校验”。测试
`TestDanglingAndConservative` 对三种情形分别断言，并额外断言不会出现用
`dangling_reference` 覆盖 `reference_unverifiable` 的情况。

### 双向引用独立判定

引用没有“传递信任”：`A->B` 命中不构成 `B->A` 有效的证据。实现上每条
`CrossTypeRef` 各自走一次索引判定，`TestBidirectionalIndependent` 构造
“正向有效、反向悬空”以及“双向都有效（两条独立 resolved）”两种情形。

## 4. 拒绝优先级与命中报告

命中类别（互不混报）与优先级：

```
out_of_range > integrity_failure > count_mismatch
              > dangling_reference / reference_unverifiable
```

一次请求可以同时命中多类问题，实现**全部保留**（不笼统报告“导出损坏”），
仅按优先级+块位置做**稳定排序**以保证输出确定；每条 `Issue` 都携带
`ChunkRef` 与具体原因，引用类问题还携带具体引用三元组。
`TestRejectionPriority` 断言顺序与五类同时存在的情形。

## 5. 聚合视图门槛与部分可用性

聚合只在全部参与类型满足以下条件时生成（`Aggregatable=true`）：

- 都在导出覆盖范围内；
- 每个块 integrity 通过且数量一致；
- 这些块的全部跨类型引用都可解析（无悬空，也无“无法校验”）。

任一不满足 ⇒ 聚合视图**整体**不可生成。但这不影响 `Load` 单独取出已通过
校验的块：`LoadResult.Reports[ref].Records` 对 trusted 块始终可用
（`TestIntegrityFailure`、`TestDanglingAndConservative` 均断言了
“聚合失败但干净块仍可取/可单独聚合”）。

## 6. 并发、幂等与确定性

- `Loader` **不持有可变状态**，也不做任何进程内缓存：每次请求重新从磁盘
  读取并独立判定，天然支持多 goroutine 对同一落盘导出并发只读校验。
- 全程不写磁盘；`TestConcurrentLoadsAreDeterministic` 在 32 个 goroutine
  下比对加载/聚合结论的规范化字符串完全一致，并比对校验前后导出文件的
  指纹证明**幂等不改盘**，再串行复跑证明**跨运行可重复**。
- 输出确定性来自：类型去重排序、块按 `(type, chunk_index)` 排序、
  记录按 JSON 数组原序遍历、问题稳定排序。

## 7. 规模无关的引用查找（性能要求）

`index.go` 使用开放寻址（FNV-1a + 线性探测）哈希集合，负载因子 ≤ 0.5。
判定单条引用是否悬空只做期望 O(1) 次探测，**不随目标块总记录数 N 线性
增长**。这不是口头承诺：

- `Contains` 返回本次真实探测槽位数，索引累计 `TotalProbes/MaxProbes`；
- `TestReferenceLookupScaleIndependent` 在 N=10²…10⁶ 上打印平均/最大
  探测并硬断言：平均 < 3、最大 ≤ 100、N 扩大 10⁴ 倍平均探测漂移 < 1。

本地实测一次：

```
N=100      avg=1.43 max=6
N=1,000    avg=1.87 max=14
N=10,000   avg=1.31 max=7
N=100,000  avg=1.63 max=11
N=1,000,000 avg=1.73 max=23
```

被放弃的方案：对目标块做线性扫描——实现简单但单次判定 O(N)，直接违反
规模无关要求（该 O(N) 做法只保留在 `naive.go` 中作为差分测试的独立实现）。

## 8. 独立朴素模型与差分测试

`naive.go` 刻意用不同实现方式复现同一规格：流式 `json.Decoder` 解析、
手写 `hash.Hash` 累加校验和、引用解析线性扫描、状态流转独立推导；它与主
实现**只共享磁盘格式约定**，不共享任何判定代码。
`TestRandomDifferential` 用固定随机种子构造 300 组场景
（每块随机选择：无损坏/篡改记录/破坏 JSON/改声明条数/截断/增补，随机生成
可能悬空的引用，随机请求集合含范围外类型），逐类比对两套实现的：

- 问题类别、命中块、引用目标的多重集合；
- 每块 trusted/integrity/count 状态；
- 聚合可生成性。

任一分歧即失败并打印种子场景，可复现。

## 9. 日志

`DecisionLogger` 接口接收结构化 `Decision{Stage, Chunk, Input, Output,
Basis}`。导出、块校验、每条引用判定、聚合判定均落日志；测试 logger
（`sliceLogger.print`）通过 `t.Logf` 打印每次判定的**输入、输出与依据**，
`go test -v` 可见。

## 10. 被放弃方案汇总

- 块头纳入 checksum：会混淆“完整性失败”和“数量不一致”，放弃。
- 数量不一致时放行“看起来完好”的记录：违反整块不可信要求，放弃。
- 目标块不可信时按悬空或按有效处理：武断结论，统一保守为“无法校验”。
- 进程内缓存校验结论加速重复加载：引入可变状态与并发一致性负担，且无法
  证明“外部改动后结论仍新”，放弃；改为无状态重读（O(1) 索引每次重建，
  开销只与块大小线性相关，且构建不在单条引用判定路径上）。
- 引用解析线性扫描：违反规模无关要求，仅作为朴素对照保留。

## 11. 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin
go test -race -v ./snapshot          # 全部用例 + 判定日志
go test -run TestRandomDifferential -v ./snapshot   # 随机差分对照
go test -run TestReferenceLookupScaleIndependent -v ./snapshot
go vet ./... && gofmt -l .
go test -cover ./...                 # 当前覆盖率约 90%
```
