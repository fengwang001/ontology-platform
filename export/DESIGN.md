# 增量导出去重与位点推进组件 — 设计说明

面向本体平台多周期增量导出：每个周期从上一次**已确认**的结束位点开始，输出区间内新写入；
中断后重放必须与“从未中断”逐条一致；位点介质损坏时仅靠历史增量记录也能推导出
**不晚于**真实已确认位点的安全起点。

## 组件划分

三类职责分别落在三个文件，经由 `Manager` / `Cycle` 协作：

| 职责 | 类型 | 文件 |
| --- | --- | --- |
| 位点声明与确认 | `Manager.Begin` / `Cycle.Confirm`、`CheckpointStore` | `manager.go` `cycle.go` `checkpoint.go` |
| 历史去重判定 | `Deduper`（周期内）、`Sink` 幂等契约（跨重放） | `deduper.go` `types.go` |
| 位点不可读时的安全推导 | `SafeStart` + `HistoryStore` 密封纪元 | `derive.go` `history.go` |

一个导出**链路 (link)** 是一个独立消费方的位点/历史命名空间；同一数据源上可并发任意多条链路。

## 核心协议

区间统一为半开 `(Start, End]`，位置为单调整数。

1. **声明**：`Begin(link, Range{Start,End})` 先读该链路最后确认位点。
   - 可读：`Start` 必须严格等于该位点，否则 `StartMismatch`，禁止凭空选起点。
   - 不可读/有歧义：转 `SafeStart` 从历史推导；推导出的安全起点与声明不符同样判 `StartMismatch`。
2. **接收**：`Accept` 以 `Write.ID` 为键。同一未确认周期内的重复提交
   **只保留首次接受的槽位**；重试夹在其他写入之间、无论何时到达，都不改变顺序也不留痕迹。
3. **交付**：`Deliver` 先写历史记录再调用 `Sink.Output`（先日志后投递：
   崩溃只会造成重复投递，绝不丢写入）。`Sink` 必须按 `Write.ID` 幂等。
4. **确认**：`Confirm` 先校验 `(Start,End]` 每个整数位置恰好一条、ID 唯一；
   然后**先存位点、再密封纪元**。任一步失败，结束位点都未被确认，
   下一个周期从上一个确认位点完整重放。

“先存位点、后密封”是刻意的：它让“已密封纪元”恒等于“已确认纪元”，
`SafeStart` 因此可以无条件信任密封纪元。两步之间崩溃只会让推导停在更早的位点（只会多重复）。

纪元号由 `MemHistory.NextEpoch` 保证在链路内**永不复用**，包括中断纪元；
否则中断纪元遗留的记录会污染后来同号的密封纪元（差分测试实际抓到过该缺陷）。

## 位点不可读时的安全推导（`SafeStart`）

只使用密封纪元（未确认纪元的记录不可信，直接忽略），按纪元顺序：

- 第一个密封纪元以其最早记录位置 `-1` 锚定起点；
- 后续纪元以前一个密封纪元的 `End` 为期望起点（纪元号可以不连续，中断纪元合法缺席）；
- `verifyContiguous` 逐条校验：位置全部落在 `(prevEnd,end]`、无重复位置、
  记录数恰为 `end-prevEnd`（顺序扫描即可发现删除/空洞）、同一 ID 不绑定两个位置。

一旦发现缺口，返回**缺口之前**已完整证明的位置 + `HistoryGap`。
该位置不可能晚于真实确认位点，因此结论是“宁可重复检查，绝不遗漏”。
连一条可证明的密封纪元都没有时，退回创世位点 `0`。

## 错误分类与固定优先级

四类错误互斥，`errors.go` 中 `Classify` 按固定顺序仲裁：

1. `StartMismatch`（声明起点 ≠ 上一次确认终点）
2. `CheckpointUnreadable`（位点介质不可读/有歧义）
3. `HistoryGap`（推导时发现历史增量记录有缺口）
4. `ResourceExhausted`（投递/存储资源不足，中止且不确认）

理由：起点声明是关于区间的逻辑事实，区间错了后续一切结论都无意义，必须最先判定；
位点读不出来才需要查历史，故其次；历史缺口只可能在这条推导路径上发现；
资源不足只有在区间合法、开始投递之后才有意义，排最后。判定以结构化的
`Error.Kind` 为准而非错误文本，顺序可被测试直接复核（`TestErrorPriorityFixed`）。

## 去重开销与历史总量无关（可复核）

- 周期内：`Deduper` 是一张以 `Write.ID` 为键的哈希表，`Accept` 平均 O(1)；
  确认后整表丢弃。表的规模只等于“当前未确认周期”这一**有限窗口**，
  与该链路累计输出过多少历史无关。
- 跨重放：重放窗口只覆盖“上一个确认位点 → 本次结束”的未确认区间，
  消费方按 `Write.ID` 幂等，无需扫描全部历史。
- 复核方式：`BenchmarkAccept` 在 0 / 1,000 / 100,000 条既有历史下
  分别为 ~266 ns/op、304 B/op，完全持平（`go test -bench=. -benchmem`）。

## 并发模型

- `Manager` 为每条链路维护独立的 `linkState` 锁、独立位点键、独立纪元序列；
  链路 A 的推进不阻塞也不影响链路 B。
- 同一链路同一时刻只允许一个未确认周期；`Begin/Confirm/Abandon` 串行化，
  保证链路内部可观察结果等价于该链路单独串行执行（`TestConcurrentLinksIsolation`）。
- 历史与位点存储内部自带互斥，共享同一数据源也可安全并发。

## 关键取舍与被放弃的方案

- **放弃“先密封后存位点”**：会使密封纪元可能未真正确认，推导误信，已被差分测试证伪。
- **放弃按纪元号连续性判断缺口**：中断纪元天然缺号；改为按密封端点链式衔接。
- **放弃扫描全量历史做去重**：开销随历史线性增长；改为周期内哈希表 + 消费方按 ID 幂等。
- **放弃组件内实现“恰好一次投递”**：在 sink 可能已收数据但应答丢失时，
  没有组件能单方面消除重复投递；因此把边界明确为“确认位点恰好一次推进 +
  投递至少一次 + Sink 按 `Write.ID` 幂等”，端到端恰好一次由二者共同保证。
- **首次接受顺序**：顺序锚定“首次被组件接受”的时刻；数据源按日志序提交，
  重试时机任意，从而保证任何重放下输出序列确定一致。
- 存储使用内存实现（`MemCheckpoint`/`MemHistory`）演示协议；
  接口化设计允许替换为带 fsync 的持久介质，`Corrupt`/`Erase` 用于故障注入测试。

## 本地验证

```bash
# 需要 Go 1.26+
go test -race -v ./export/
go vet ./...
gofmt -l .

# 去重开销不随后台历史增长的可复核证据
go test -bench=. -benchmem -run=^$ ./export/

# 生成一次示例运行的输入/输出/判定依据日志
EXPORT_JOURNAL=1 go test -run TestWriteSampleJournal ./export/
cat export/testdata/sample-journal.log
```

测试矩阵：

- `TestCleanCycle` / `TestConfirmRejectsIncomplete`：确认前后状态；
- `TestInterruptRestartEquivalence`：接受后、投递中途、投递后、确认失败四个时刻中断后重放；
- `TestRetryKeepsFirstSlot` / `TestDeduperOrdering`：重试夹序与槽位固定；
- `TestSafeStartFromHistory` / `TestSafeStartGenesisFallback` / `TestSafeStartHistoryGap`：
  介质损坏推导、创世回退、历史缺口停在缺口之前；
- `TestErrorPriorityFixed`：四类错误固定优先级；
- `TestConcurrentLinksIsolation`：三条链路并发互不干扰且各自等于串行结果；
- `TestManyInterruptionsCumulativeEquivalence`：多次中断后累计结果与持续输出逐条比对；
- `TestRandomizedDifferential`：300 个固定种子随机构造的中断/重试/介质损坏序列，
  与 `naiveModel`（线性扫描、逐步独立判定的朴素参照模型）对照，
  每个 `begin/accept/confirm` 的输入、输出、判定依据写入 `journal`，
  任何分歧都直接打印完整日志并可按种子重放。
