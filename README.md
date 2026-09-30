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

## 一致位点导出器（`exporter` 包）

支持在写入持续进行时导出：每个导出会话先吐**快照段**，再吐**增量段**，
两段无缝拼接，每条记录恰好出现一次且全局升序、结果可复现。

### 序号划分规则

- 每条写入先校验（键非空），校验通过才分配连续递增序号（从 1 开始）；
  被拒绝的写入不占用序号。
- 会话调用 `Begin()` 时记录**起点序号** `S`（即当前已分配的最大序号）。
- **快照段**：序号 `1..S`，这些记录在 `Begin` 时已存在，必定能发出。
- **增量段**：序号 `S+1` 起，只在下一条待发序号对应的记录已写入时才发出，
  严格按序、不跳读；记录尚未写入时 `NextN` 返回空，调用方稍后重试。
- 段归属只取决于序号与会话起点，与记录到达/读取先后无关；
  不同会话起点不同，同一序号在不同会话中可属不同段。
- `End()` 报告两段区间 `[First, Last]` 并核验无缝：
  快照段从 1 开始、增量段起点等于快照段终点 +1、段内序号连续。

### 拒绝语义（可区分、且原子）

| 场景 | 错误 |
| --- | --- |
| 空键写入 | `ErrEmptyKey` |
| 未开始就取数/结束 | `ErrExportNotStarted` |
| 已开始再开始 | `ErrExportAlreadyStarted` |
| 已结束再取/再开始/再结束 | `ErrExportAlreadyEnded` |
| 未结束导出数超限 | `ErrExportLimitExceeded` |

任何一次失败都是整体拒绝：不改变日志、序号与任何会话的已发序列。

### 本地验证：从头顺序读核对

`Log.ReadAll()` 从头顺序读出全部记录（序号 1 到最大，无缺口）。
核对一次导出是否正确的本地方法：

1. 会话 `Begin()` 后循环 `NextN` 收集记录，直到集满目标条数；
2. 拼接快照段与增量段，断言第 `i` 条序号为 `i+1`（升序、恰好一次）；
3. 与 `ReadAll()` 的结果逐字段比对，二者必须完全一致；
4. `End()` 返回的 `Report.Verify()` 必须为 nil。

可参考 `exporter/exporter_test.go` 中的
`TestConcurrentExportsAreIdentical`（并发下两会话独立导出逐字段相同，
并与从头顺序读核对）。运行：

```bash
go test -race -v ./exporter/
```
