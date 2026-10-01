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

## 多列族 WAL 回收水位（`walreclaim` 包）

`walreclaim.Manager` 跟踪各列族未落盘内存表（活跃表 + 冻结队列）对 WAL 日志文件的依赖，
保证“可回收日志编号集合”始终恰好等于仍无任何未落盘数据依赖的前缀。

### 编号与 `first` 取值规则

- 日志编号从 `1` 起，`cur` 初始为 `1`；`Roll()` 使 `cur` 加一。
- 每个列族有一个活跃内存表（可空）和按冻结先后排列的冻结队列。
- 内存表的 `first` 为它**首次被写入时的 `cur`**：
  - `Write` 时若活跃表为空，则新建活跃表并把当时的 `cur` 记为 `first`；
  - 活跃表非空时后续写入不改变 `first`；
  - `FlushStart` 只冻结非空活跃表，冻结后活跃表变空；冻结之后新建的活跃表
    在下一次 `Write` 时才取当时的 `cur`（仅 `Roll` 不预取）；
  - `FlushDone` 移除队首（最旧）冻结表；`DropCF` 使该列族全部内存表作废，
    名字随后可重新创建为全新列族。

### 回收条件

日志 `w` 可回收（`Obsolete`）当且仅当同时满足：

1. `w < cur`：当前日志（`== cur`）永不可回收；
2. `w` 尚未被 `Purge`（已回收集合恒为编号前缀，内部用高水位表示）；
3. `w` **严格小于**全部未落盘内存表（含活跃与冻结，不含已作废）`first` 的最小值；
   即 `w` 恰等于某表 `first` 时仍不可回收；没有任何未落盘表时不受此限。

`Purge` 把当前可回收编号标记为已回收并升序返回；之后 `Obsolete` 不会重复返回它们。
所有方法由互斥锁串行化，并发调用结果等价于某个串行顺序。

### 拒绝原因（同一次调用按此顺序只报第一个，且不改状态）

| 哨兵错误 | 触发条件 |
| --- | --- |
| `ErrEmptyName` | 列族名为空（优先于一切） |
| `ErrExists` | `CreateCF` 重名 |
| `ErrNotFound` | 列族不存在（优先于下面两个前置条件） |
| `ErrEmptyActive` | `FlushStart` 时活跃表为空 |
| `ErrEmptyFlushed` | `FlushDone` 时冻结队列为空 |

### 本地验证

```bash
# 全部测试（含 3000 步朴素实现对拍、确定性重放、并发安全）
go test ./walreclaim/

# 竞态检测
go test -race ./walreclaim/

# 查看对拍日志：每步打印输入、实际/参照输出与判定依据
# （cur、全部 first 多重集合、已 Purge 高水位、当前 obsolete）
go test -run TestRandomDifferential3000 -v ./walreclaim/

go vet ./...
gofmt -l .
```

若环境的默认 Go 构建缓存目录不可写，可指定 `GOCACHE=/tmp/gocache`。
