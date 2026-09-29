# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 大值溢出存储（`overflow` 包）

值超过字节阈值时溢出到溢出表，主记录只存块号引用；不超过阈值时内联存主记录。两种形态互斥。

### 阈值边界

- `len(value) <= Threshold`：内联存主记录。
- `len(value) > Threshold`：分配单调递增、永不复用的块号写入溢出表，主记录只存块号。
- `Threshold`、`MaxBlocks` 必须大于 0，否则 `Open` 拒绝并返回 `ErrInvalidThreshold` / `ErrInvalidMaxBlocks`。
- 空键、溢出块数超限的写入被整体拒绝（`ErrEmptyKey` / `ErrBlockLimit`），失败不改变主记录、溢出表与块号计数。

### 更新顺序规则（崩溃安全）

更新大值严格按以下顺序执行：

1. **先写新块**：把新值写入新块文件并 fsync；
2. **再更新引用**：原子地（临时文件 + rename + fsync）持久化主记录中的新块号；
3. **最后回收旧块**：删除旧块文件。

因此在任意两步之间崩溃，引用都不会指向不存在的块——最坏情况只是留下一个无引用的孤儿块。删除键时同样先持久化记录删除、再回收其溢出块。

### 崩溃恢复

`Open` 时扫描溢出表目录：

- 回收所有无引用的孤儿块（可通过 `RecoveredOrphans` 查看）；
- 检出指向不存在块的悬挂引用，返回 `ErrDanglingRef` 并拒绝打开；
- 块号计数器取持久化值与盘上最大块号 +1 的较大者，保证块号单调递增、永不复用。

任意时刻的不变量（`Verify` 自检，可与读写并发调用）：每个引用都指向存在的块，每个块恰被一个引用引用。

### 本地验证：朴素重放核对

`TestNaiveReplay` 把同一串确定性随机操作（固定种子的 put/delete）同时施加到 `Store` 和一个朴素 `map[string][]byte` 模型上，逐步比对所有键的取值并调用 `Verify`，最后重开存储再核对一次：

```bash
go test -run TestNaiveReplay -v ./overflow
```

单测日志会打印每步操作、各键状态（内联/块号/长度）、溢出块集合与判定依据：

```bash
go test -race -v ./overflow
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
