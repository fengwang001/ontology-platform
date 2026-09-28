# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 键组划分与扩缩容状态重分配（`keygroup` 包）

### 键组映射

- 键先经 FNV-1a 哈希确定性地映射到固定数量（`maxParallelism`）的键组，
  键组编号取值 `[0, maxParallelism)`；同一输入序列必得同一映射结果。
- 键值状态按 `key -> value` 维护在 `Table` 中，每个键组在任意时刻恰好归属一个实例。

### 区间划分

- 键组按连续闭区间分给 `parallelism` 个实例，实例 `i` 的区间为：
  `start = floor((i * maxParallelism + parallelism - 1) / parallelism)`，
  `end = floor(((i+1) * maxParallelism - 1) / parallelism)`。
- 各实例区间两两不交、首尾相接，且恰好覆盖 `[0, maxParallelism-1]` 的全部键组。

### 迁移规则

- 并行度改变时逐键组比较新旧归属：归属不同的键组整组迁移，归属相同的键组原地不动。
- 迁移量即被移动键组内的键总数；扩缩容后所有键的值与集合不变（不丢不重）。
- 非法参数（`maxParallelism <= 0`、`parallelism <= 0`、`parallelism > maxParallelism`）、
  实例下标越界、空键均以可区分的哨兵错误拒绝；被拒绝的操作不改变并行度、键值或分桶。
- `Table` 内部以读写锁保护，`Snapshot()` 返回深拷贝的一致性快照，支持并发读取。

### 本地验证

```bash
# 运行键组组件全部测试（含竞态检测与输入/归属/迁移日志）
go test -race -v ./keygroup/
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
