# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 一致位点导出器（`ontology` 包）

支持写入持续进行时导出：先吐**快照段**再吐**增量段**，两段无缝拼接；
每条记录恰好出现一次、严格按序号全局升序，多个并发导出会话的完整结果逐字段相同。

### 序号分配规则

- `Log.Append` 先做校验，通过后才分配连续递增序号（从 1 开始）。
- 键为空（`ErrEmptyKey`）或日志已密封（`ErrLogSealed`）时整体拒绝，**不分配序号**、不改变日志。

### 快照段与增量段的序号划分

- `ExportSession.Start` 在开始导出的瞬间记录起点序号 `split`（当时日志末端）。
- **快照段**：`1 .. split`，即开始时已存在的记录，按序发出。
- **增量段**：`split+1 ..`，即开始之后才写入的记录；只有当“下一条待发序号”对应的记录
  已经存在时才发出，严格按序、不等待、不跳读（`Next` 返回 `ok=false` 表示该条尚未产生）。
- 段归属**只按序号**（`seq <= split` 为快照，否则增量），与记录何时被读到无关。
- 记录按连续序号追加，因此序号小的必先存在，快照段与增量段天然无缝，无空洞无重复。
- `End` 报告两段实际区间（`Report{Snapshot, Incremental, Split}`）并核验：
  快照必须是完整前缀 `1..split`，增量必须紧接 `split+1` 连续；
  日志未密封时无法保证完整性，返回 `ErrLogStillOpen`。

### 拒绝原因（可区分，且一次失败不改变任何状态）

| 场景 | 错误 |
| --- | --- |
| 空键写入 | `ErrEmptyKey` |
| 密封后写入 | `ErrLogSealed` |
| 未 `Start` 就 `Next`/`End`/`Report` | `ErrExportNotStarted` |
| 已 `Start` 再 `Start` | `ErrExportAlreadyStarted` |
| 已 `End` 再 `Next`/`Start`/`End` | `ErrExportEnded` |
| 未结束导出会话数超上限（默认 8） | `ErrExportLimitExceeded` |
| 两段区间不满足无缝拼接 | `ErrSeam` |
| 日志仍开放写入时做完整核验 | `ErrLogStillOpen` |

### 本地验证方法

从头顺序读是核对导出结果的基准：密封日志后，用 `At(1)..At(LastSeq)` 逐条顺序读取，
与导出会话发出的序列逐字段比较，应完全一致（见 `ontology/export_test.go` 的
`TestReadFromHeadVerification` 与 `TestConcurrentSessionsReproducible`）。

```bash
# 详细日志：动作 / 所处段 / 发出序号 / 判定依据
go test -race -v ./ontology
```

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
