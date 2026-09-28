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

## 键组划分与扩缩容状态重分配（`keygroup` 包）

### 键组映射

- 键先经确定性哈希映射到固定数量的键组：`group = fnv32a(key) % maxParallelism`（`KeyGroupOf`）。
- `maxParallelism` 即键组总数，创建组件时确定且之后不变；哈希函数确定，同一输入序列反复计算得到完全相同的输出。

### 区间划分

- 键组按连续区间分给 `parallelism` 个实例（`ComputeKeyGroupRange`，与 Flink `KeyGroupRangeAssignment` 一致）：
  - `start = (index*maxParallelism + parallelism - 1) / parallelism`
  - `end   = ((index+1)*maxParallelism - 1) / parallelism`
- 各实例区间两两不交、首尾相接，且恰好覆盖 `[0, maxParallelism-1]`。

### 迁移规则

- `Rescale(newParallelism)` 逐键组比较调整前后的归属实例：
  - 归属变化的键组**整组迁移**，归属不变的键组**原地不动**；
  - 迁移量 = 被移动键组内的键数总和（`MigrationReport.MovedKeys`）；
  - 扩缩容后所有键的值与集合不变（既不丢也不重），结果见 `MigrationReport` 与一致性 `Snapshot`。
- 非法输入（`maxParallelism <= 0`、`parallelism` 越界、实例下标越界、空键）一律拒绝，
  并以可区分的 sentinel 错误返回（`ErrInvalidMaxParallelism` / `ErrInvalidParallelism` /
  `ErrInvalidOperatorIndex` / `ErrEmptyKey`，用 `errors.Is` 判定）；
  被拒绝的操作不改变当前并行度、键值或分桶。
- 所有方法可并发调用；`Snapshot` 返回深拷贝，逐字段一致。

### 本地验证

```bash
# 组件测试（含竞态检测，日志打印输入、归属、迁移结果及判定依据）
go test -race -v ./keygroup/

# 全量测试与静态检查
go test ./...
gofmt -l .
go vet ./...
```
