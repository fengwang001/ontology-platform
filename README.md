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

`keygroup` 包把"按键维护的状态"经固定数量的键组映射到若干实例，支持在并行度
（实例数）变化时只迁移归属发生变化的键组，保证扩缩容前后所有键的状态不丢、不重。

### 映射与划分规则

- **键 → 键组**：键经 FNV-1a（32 位）确定性哈希映射到 `[0, numGroups)` 内的
  固定键组；键组数量在 `Store` 生命周期内不变，因此同一个键永远落在同一个键组，
  与并行度无关。空键一律拒绝（`ErrEmptyKey`）。
- **键组 → 实例**：`AssignRanges(numGroups, parallelism)` 把键组按连续区间
  `[start, end)` 分给实例，端点为 `end_i = floor((i+1) * numGroups / parallelism)`。
  区间两两不交、首尾相接、恰好覆盖 `[0, numGroups)`，且各区间宽度相差不超过 1
  （尽量均匀）。`OwnerOf` 与该划分严格一致。
- **约束**：并行度必须为正整数，且不得超过键组数量（`1 <= parallelism <= numGroups`）。

### 扩缩容与迁移规则

- 调用 `Store.Rescale(newParallelism)` 时，对每个键组比较其旧归属实例与新归属
  实例：归属不同则**整组迁移**，归属相同则**原地不动**。
- `RescaleResult` 给出逐键组的 `OldOwner/NewOwner/Moved/KeyCount`、迁移键组列表
  `MovedGroups`、迁移键数 `MovedKeys`（所有被迁移键组内的键数之和）与总键数。
- 迁移的只是"归属关系"，键到键组的映射与键值均不变；因此扩缩容后键集合与每个
  键的值完全守恒（不丢、不重）。
- 结果确定：哈希、区间端点与迁移判定全部是纯函数，同一输入序列反复计算得到完全
  相同的输出（测试 `TestRescaleConservation` 中重建 Store 比对验证）。
- 非法操作（空键、并行度非正、并行度越界、并行度不变）通过可区分的哨兵错误
  返回：`ErrEmptyKey`、`ErrInvalidGroups`、`ErrInvalidParallelism`、
  `ErrParallelismTooLarge`、`ErrParallelismUnchanged`、`ErrInvalidLogger`；
  被拒绝的操作先校验后变更，不改变当前并行度、键值与分桶（可用 `errors.Is` 判定）。

### 并发与一致性

`Store` 内部使用 `sync.RWMutex` 保护，`Snapshot()` 在单次读锁内拷贝区间、键值与
每键归属，返回逐字段一致的只读视图；扩缩容可与任意数量的并发读同时进行
（`go test -race` 验证）。

### 日志

每次扩缩容都会通过注入的 `Logger`（可用 `keygroup.NewLogger(io.Writer)` 适配
标准库日志）打印：输入参数（键组数、新旧并行度、当前键数）、新旧区间、每个键组的
前后归属与"整组迁移/原地不动"判定及依据，以及最终迁移键组与迁移键数汇总。

### 快速上手

```go
logger := keygroup.NewLogger(os.Stdout)
store, err := keygroup.NewStore(128 /* 键组数 */, 4 /* 初始并行度 */, logger)
if err != nil {
    log.Fatal(err)
}
_ = store.Put("user:1", "state")

result, err := store.Rescale(8) // 扩容到 8 个实例
// result.MovedGroups / result.MovedKeys 即最小迁移集与迁移量
snap := store.Snapshot()        // 一致只读视图：Ranges / KeyOwners / Values
```

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 覆盖率（当前 keygroup 包覆盖率约 94%）
go test -race -cover ./...

# 只跑键组相关用例并查看判定日志
go test -race -v ./keygroup -run 'TestRescaleConservation|TestAssignRangesPartition'

# 格式与静态检查
gofmt -l .
go vet ./...
```

若环境中 `go` 不在 PATH（如 `/usr/local/go/bin`）或默认构建缓存目录只读，可：

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache
```
