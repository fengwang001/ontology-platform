# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 分区保序路由器（`ontology` 包）

`ontology/router.go` 实现了一个并发安全的分区保序路由器，接收
`Event{Partition, Offset, Value}` 形式的变更事件并维护跨分区统计。

### 位点连续性

- 每个分区的位点必须从 `0` 开始、严格连续递增，无空洞、不允许重放。
- 分区 `p` 已接受 `n` 条时，只接受 `Offset == n` 的下一条事件；否则以
  `ReasonOffsetGap` 拒绝。
- 分区号越界（`< 0` 或 `>= 分区数`）以 `ReasonPartitionOutOfRange` 拒绝；
  `Value < 0` 以 `ReasonNegativeValue` 拒绝。三类原因可通过
  `*ontology.RejectError` 的 `Reason` 字段区分，且 `errors.Is(err, ErrRejected)` 成立。
- 任何拒绝都是原子的：`Feed` 失败不改变状态；`FeedBatch` 在写入前先对整批做
  投影校验，任一事件非法则整批拒绝，分区缓冲与全部统计量保持不变。
- 跨分区事件可以任意交错（含并发 goroutine、整批混排）；因为统计只依赖每分区
  位点 → 值的映射，终值与喂入顺序无关，始终可复现。

### 统计量与水位

`Snapshot()` 与 `SelfCheck()` 返回字段完全一致的 `Stats`：

- `PartitionCounts[p]` / `PartitionSums[p]`：分区 `p` 已接受的条数与值之和。
- `GlobalSum`：所有分区值之和，等于各 `PartitionSums` 之和。
- `Watermark`：所有分区都已对齐到的最小前缀长度，即
  `Watermark = min_p PartitionCounts[p]`。含义是「每个分区都至少有
  `Watermark` 条事件」，因此 `[0, Watermark)` 是确定无空洞的公共前缀。
  某个分区落后时，水位由最短的分区决定，其它分区超出水位的部分尚未“提交”。
- `CommittedSum`：已提交前缀和，即每个分区 `[0, Watermark)` 区间值之和的总和。
- 以上统计量随喂入单调不减（读取使用读写锁，快照为一致拷贝，可并发调用）。

`SelfCheck()` 除返回快照外，还基于每分区内部缓冲逐项重放前缀和、重算全局总和、
  最小前缀水位与已提交前缀和并逐字段比对，任一不符即返回错误作为判定依据。

### 与按位点批量重算对照的本地验证

`go test -v ./ontology` 会打印每个用例的输入、各分区计数、水位与判定依据。
`TestBatchRecomputeCrossCheck` 采用不依赖路由器的独立重算：

1. 把事件按 `(分区, 位点)` 写入独立二维缓冲 `grid[p][offset]`；
2. 每轮用随机交错的批次喂入路由器；
3. 直接遍历 `grid` 重算各分区计数/和值、`min` 水位，并累加每分区
   `[0, watermark)` 得到已提交前缀和；
4. 与路由器快照逐字段比对。

另可用以下命令做完整本地验证：

```bash
# 交错顺序复现 + 拒绝原子性 + 并发喂入/读取 + 批量重算对照
go test -race -v ./ontology

# 只看重算对照用例的日志（输入/计数/水位/判定依据）
go test -run TestBatchRecomputeCrossCheck -v ./ontology
```
