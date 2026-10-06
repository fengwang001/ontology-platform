# logkv 设计说明

只追加日志结构键值引擎：启动恢复、撕裂尾部截断、提示信息加速、
读取自愈、段合并与合并崩溃清理。本文档记录模块划分、关键取舍、
被放弃的方案与本地验证方法。

## 模块划分

| 文件 | 职责 |
| --- | --- |
| `errors.go` | 五类可区分错误及其优先级（参数非法 > 段损坏 > 目录失真 > 段不存在 > 活动段不可合并） |
| `record.go` | 记录编解码：长度、写序号、键、值/删除标记、CRC32-Castagnoli |
| `segment.go` | 段文件抽象与流式扫描；撕裂尾部三种形态（头不完整 / 声明长度越过段末 / 最后一条校验失败）的判定 |
| `hint.go` | 提示信息编解码：每键最新记录的位置、长度、写序号、删除标记 + 段有效字节数 + 自带校验；不含值 |
| `keydir.go` | 内存键目录：每键只保留写序号最大的记录位置 |
| `store.go` | `Open` 恢复、`Get/Put/Delete`、活动段轮转、目录失真自愈、统计 |
| `merge.go` | 合并协议（临时段 → 元信息 → 原子改名封口 → 提示 → 清理）、恢复期的合并残留清理 |

协作关系：`store` 依赖 `segment`/`hint`/`keydir`/`record`；
`merge` 只通过 `store` 的内部状态与 `segment`/`record` 协作；
错误统一走 `errors.go` 的 `*Error`，调用方用 `IsKind` 判定。

## 磁盘布局

```
000001.seg        日志段（最后一个为活动段，其余已封口）
000001.hint       已封口段的提示信息（可选）
000001.mseg.tmp   合并输出的未封口临时段
000001.mmeta      合并元信息：输出段号 + 声明替代的段集合 + CRC
```

记录：`crc u32 | length u32 | seq u64 | flags u8 | klen u32 | vlen u32 | key | value`。
提示：`"LKH1" | validBytes u64 | count u32 | entries... | crc u32`。

## 关键决策

### 恢复

- 已封口段：提示信息存在、自带校验通过、且 `validBytes` 等于段文件
  实际大小（已封口段全部字节有效）时直接采用，否则作废并扫描全段，
  扫描结果为权威。采用提示的恢复只读提示文件，开销随不同键数增长。
- 已封口段扫描中任何记录无效都是段损坏，恢复整体失败并报告段号与
  首个损坏位置；`Open` 返回 nil，不产生部分状态。
- 活动段允许撕裂尾部：截断到最后一条完整记录之后，截断字节数记入
  `Stats.TruncatedBytes`；非尾部无效记录同样是段损坏。
- 恢复后 `nextSeq = maxSeq + 1`，写序号全局单调。

### 读取与自愈

- 键目录命中后按登记的位置/长度做一次 `ReadAt`：校验失败报段损坏；
  记录有效但键或写序号与目录不符是目录失真——计入 `Stats.Distortions`，
  扫描该段重建目录条目后重试，成功计入 `Stats.SelfHeals` 并返回正确
  结果，仍不一致报段损坏。失真与损坏因此可区分。
- 一次读取 = 一次哈希查找 + 一次定址读，与总键数、总段数无关
  （`Stats.RecordReads` 可验证：命中恰好 +1，未命中 +0）。

### 合并

- 输出段号复用被合并集合的最小段号，保证活动段始终是最大段号，
  且不需要额外的号源。
- 提交流程：写 `mseg.tmp` → 写 `mmeta` → 原子 `rename` 为 `.seg`
  （唯一提交点）→ 写提示 → 删除被替代段 → 删 `mmeta`。
- 恢复清理：`mseg.tmp` 存在即整体丢弃（未封口或撕裂都丢弃，保留旧段）；
  `.seg` 与 `mmeta` 并存则清理其声明替代的段；孤立 `mmeta` 直接删除。
  清理幂等，任意崩溃点重放结果一致。
- 删除标记丢弃条件：不在合并集合中的任何段里都不存在该键写序号
  更小的记录。为此扫描集合外所有段，取这些键在集合外的最小写序号。
- 键目录切换只改写仍指向被替代段的条目；并发写入落在活动段，
  不受影响。合并前后所有键的读取结果一致（被清除的删除标记除外，
  规范允许其变为"从未出现"）。

### 并发与确定性

- 一把 `sync.RWMutex`：读共享、写/合并/自愈独占。所有操作可并发，
  结果等价于某个串行顺序。`RecordReads` 用原子计数（读路径在读锁下）。
- 提示条目与合并输出均按键排序写出，文件不含时间戳与随机数：
  相同操作序列与相同崩溃点重放得到完全相同的字节与恢复结果。

## 被放弃的方案

- **输出段用全新段号**：需要活动段让出"最大段号"不变式或引入
  独立号源，崩溃恢复还要处理号源空洞；复用最小段号更简单。
- **多文件/锁分离的细粒度并发**（每段一把锁、无锁键目录）：
  正确性论证复杂，而规范只要求"等价于某个串行顺序"，单锁足够。
- **键目录持久化快照**：提示信息已承担该职责，额外快照只会引入
  第三种需要保持一致的状态。
- **合并时引用旧段记录而非重写**：跨段引用会让段删除与目录重建
  互相牵连；重写输出段使每个段自包含，恢复逻辑保持线性。
- **整段读入内存扫描**：改为 `bufio` 流式扫描，避免大段的内存峰值。

## 本地验证

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache GOPATH=/tmp/gopath
go test ./logkv            # 全量
go test ./logkv -race      # 竞态
go test ./logkv -run TestModelRandomOps -v   # 模型对照，逐条操作日志
LOGKV_SEED=42 go test ./logkv -run TestModelRandomOps -v  # 指定种子
gofmt -l logkv && go vet ./logkv
```

测试与规范的对应关系：

| 规范要求 | 测试 |
| --- | --- |
| 每个字节边界截断活动段 | `TestTruncateActiveEveryByte` |
| 已封口段每个位置翻转一位 | `TestSealedSegmentBitFlipNoHint` / `TestSealedSegmentBitFlipWithHint` |
| 撕裂尾部三种形态 vs 中段损坏 | `TestScanTornKinds` / `TestScanMidCorruption` / `TestTornTailVsMidCorruption` |
| 提示信息每个字段被篡改 / 缺失 | `TestHintTamperEveryByte` / `TestHintValidBytesMismatch` / `TestHintFieldsTampered` / `TestHintMissing` |
| 提示与段内容不符的回退 | 同上（回退后扫描结果为权威） |
| 目录失真自愈 | `TestDirectoryDistortionSelfHeal` / `TestDistortionVsCorruption` |
| 删除标记保留与丢弃 | `TestMergeTombstoneKept` / `TestMergeTombstoneDropped` |
| 被合并集合不连续 | `TestMergeNonContiguous` |
| 合并封口前后各崩溃点 | `TestMergeCrashStages`（5 个阶段） |
| 错误类别与优先级 | `TestMergeErrorKinds` |
| 提示恢复开销随键数 | `TestHintRecoveryCostScalesWithDistinctKeys` |
| 读取开销与总量无关 | `TestReadCostIndependentOfTotals` |
| 提示信息不含值 | `TestHintContentSanity` |
| 重放确定性 | `TestRecoveryReplayDeterminism` |
| 写序号单调 | `TestSeqMonotonicAcrossReopen` / `TestMergePreservesSeq` |
| 随机操作对照朴素模型 | `TestModelRandomOps` |
| 并发等价串行 | `TestConcurrentReadWrite` / `TestMergeConcurrentReadWrite`（`-race`） |
