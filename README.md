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

## 大值溢出存储（`overflow` 包）

`overflow` 包实现大值溢出存储：值超过字节阈值时溢出到溢出表，主记录只保存块号引用。

### 阈值边界

- `len(value) <= threshold`：值内联保存在主记录中。
- `len(value) > threshold`：值写入溢出表，主记录只存块号。
- 两种方式互斥：任意时刻一条主记录要么是内联值、要么是块号引用。
- 块号从 1 开始单调递增，回收后**永不复用**，因此恢复结果始终可复现。

### 更新的顺序规则

更新大值时严格按以下顺序执行，任何一步之间崩溃都不会让引用指向不存在的块：

1. **先写新块**：分配新的块号并把新值写入溢出表；
2. **再更新引用**：把主记录中的块号指向新块；
3. **最后回收旧块**：从溢出表删除旧块。

崩溃在步骤 1 之后留下一个无引用的孤儿块；崩溃在步骤 2 之后旧块成为孤儿块。两种情况的引用都始终指向存在的块。删除键时同步回收其溢出块。恢复时调用 `Recover()` 扫描并回收所有无引用的孤儿块，并检出指向不存在块的悬挂引用（报告在 `RecoveryReport.DanglingKeys` 中）。

### 失败语义

非法阈值（`ErrInvalidThreshold`）、非法块数上限（`ErrInvalidMaxBlocks`）、空键（`ErrEmptyKey`）、溢出块数超限（`ErrTooManyBlocks`）都会整体拒绝操作，且一次失败不改变主记录、溢出表与块号计数。各原因可用 `errors.Is` 区分。

### 并发

`Get`、`Block`（溢出块查询）与 `Check`（自检）均可与写操作并发调用。任意时刻满足：每个引用都指向存在的块，每个块恰被一个引用引用；`Check()` 可随时验证这两条不变式。

### 本地验证：朴素重放核对

用一个朴素的 `map[string][]byte` 模型重放同一串操作序列，与溢出存储逐键比对取值：

```bash
# 固定种子重放 500 次随机 Put/Delete，与朴素 map 模型逐键核对
go test -race -v -run TestNaiveReplay ./overflow/

# 全量测试（阈值边界、更新回收、崩溃点模拟、恢复、并发）
go test -race -v ./overflow/
```

测试日志会打印每步操作、各键状态、溢出块集合与判定依据；重放使用固定随机种子，结果可复现。
