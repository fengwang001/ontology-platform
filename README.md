# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 首写胜出（First-Write-Wins）去重寄存器

`register` 包实现了一个并发安全的去重寄存器（`register.Register`）：每个键的生效值
始终是迄今见过的**逻辑序号最小**的写入。逻辑序号代表写入的逻辑先后，序号越小越早，
因此结果与写入到达顺序无关、可复现。

### 写入判定规则

- **建立（establish）**：键尚无生效值时，写入直接建立生效值。
- **撤回后建立（withdraw → establish）**：新写入序号严格小于当前生效序号时，先向
  变更日志追加一条 `withdraw`，再追加 `establish`。`withdraw` 的键、序号、值必须
  恰好匹配当时已物化的那一条写入。
- **后写落败丢弃（late loss）**：新写入序号严格大于当前生效序号时直接丢弃，计入
  丢弃计数（`Discarded`），不产生任何变更日志。
- **序号重复拒绝**：同一键的序号与当前生效序号相同（无论发生在批内还是针对已物化
  状态）均拒绝。

### 拒绝原因与批量原子性

以下情况以可区分的哨兵错误整体拒绝，可用 `errors.Is` 判定：

- `register.ErrEmptyKey`：键为空或仅空白字符。
- `register.ErrInvalidSeq`：逻辑序号非正（`<= 0`）。
- `register.ErrDuplicateSeq`：同一键序号与当前生效序号重复（含批内自重复）。

错误会包装为 `*register.RejectError`（可用 `errors.As` 取得），给出批内下标与写入
内容。一批写入中任一条被拒，整批不生效：状态、变更日志、丢弃计数均保持不变
（先全量校验、后应用）。

### 变更日志与自检

- `ChangeLog()` 返回 append-only 日志的副本，条目为 `establish` 或 `withdraw`。
- `Verify()` 重放整条日志重建视图，并校验每条 `withdraw` 与当时物化条目逐字段匹配、
  每次建立的序号严格更小，最终与当前物化视图一致。
- 不变量：任一键的生效序号随写入只减不增；并发读取（`Lookup` / `Snapshot` /
  `Discarded` / `ChangeLog` / `Verify`）由 `sync.RWMutex` 保护，每次快照都是某一
  时刻自洽、逐字段一致的深拷贝视图。

### 本地验证方法

```bash
# 竞态检测 + 详细日志（含写入、变更日志、生效值、判定依据）
go test -race -v ./register

# 重复运行以压测并发路径
go test -race -count=10 ./register
```

核对结果的独立方法（逐键取最小序号）：对每个键收集所有已接受写入的序号，生效序号
必须等于该集合的最小值；等价地，重放变更日志时只保留每键最小的 `establish`，
重建视图应与 `Snapshot()` 完全一致（`Verify()` 即按此思路实现，测试
`TestConcurrentReadersConsistentView` 末尾也用日志独立重算了逐键最小序号做交叉核对）。

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
