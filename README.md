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

## WAL 回收水位管理器（`wal` 包）

多列族预写日志回收水位管理器，跟踪各列族未落盘内存表对日志文件的依赖，
使可回收的日志编号集合始终恰好等于仍无任何未落盘数据依赖的前缀。

### 回收条件

日志文件编号从 1 起，当前日志编号 `cur` 初始为 1，`Roll` 使 `cur` 加 1。
日志 `w` 可回收当且仅当同时满足：

- `w < cur`（当前日志永不可回收）；
- `w` 尚未被 `Purge`；
- `w` 严格小于全部未落盘内存表（含活跃表与冻结队列，不含已 `DropCF` 作废的表）
  的 `first` 的最小值；没有任何未落盘表时不受此限。

`Obsolete` 返回当前可回收编号（升序快照，不改变状态）；
`Purge` 把当前可回收编号全部标为已回收并返回，之后 `Obsolete` 不再重复返回。

### `first` 的取值规则

- 每个内存表的 `first` 是它**首次被写入时**的 `cur`；
- 空活跃表没有 `first`，不参与回收判断；
- `FlushStart` 把非空活跃表冻结入队尾（`first` 不变，仍阻碍回收直到 `FlushDone`），
  活跃表随之变空；冻结后新建的活跃表在**下一次写入时**才取当时的 `cur` 作为 `first`；
- `FlushDone` 移除队首（最旧）冻结表，解除其 `first` 的阻碍；
- `DropCF` 作废该列族全部内存表，立即解除阻碍，该名可重新创建为全新列族。

### 错误语义

列族名为空、重名创建、列族不存在、`FlushStart` 时活跃表为空、
`FlushDone` 时冻结队列为空，分别对应可区分的哨兵错误
（`ErrEmptyCFName`、`ErrCFAlreadyExists`、`ErrCFNotFound`、
`ErrActiveMemtableEmpty`、`ErrNoFrozenMemtable`，可用 `errors.Is` 判断）。
同一调用上按此顺序只报第一个；被拒绝的操作不改变任何状态。

### 并发与确定性

所有方法可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序；
任何时刻已回收编号集合不含仍被未落盘表依赖的日志；
相同调用序列重放得到完全相同的 `Obsolete` 与 `Purge` 结果。

### 本地验证

```bash
# 全部单元测试（含边界、错误、并发安全、确定性重放）
go test ./wal/

# 与朴素实现（每次遍历全部内存表重算）对拍 3000 步随机操作，
# -v 日志中打印每步的输入、输出与判定依据
go test -v -run TestRandomDifferential ./wal/

# 竞态检测
go test -race ./wal/
```
