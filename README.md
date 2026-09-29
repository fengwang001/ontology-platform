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

## 快照 + 增量双读一致性读取器（`snapread` 包）

`snapread.Store` 在写入持续进行时提供可冻结、可复现的历史位点读：

- 写入：`Write(key, value)` 向只增日志追加一条记录，序号从 1 起单调递增。
- 快照：`Snapshot()` 冻结当前序号 `S`，把逐键最新值深拷贝成不可变副本；
  日志保留全部记录、从不清空，可反复调用。
- 位点读：`ReadAt(seq, key)` 以快照副本为基，再按序应用快照点之后的增量
  （同键最新写胜出），返回该键在 `seq` 时刻的值或“不存在”。

### 位点区间与边界

设最近一次快照点为 `S`，当前已写入位点为 `H`：

| 位点区间 | 行为 |
| --- | --- |
| `seq < S` | 整体拒绝，返回 `ErrSeqBeforeSnapshot`（该区间已被快照截断） |
| `seq == S` | 只取快照基；增量区间 `(S, S]` 为空——快照点恰好等于某次写入时，该次写入已包含在快照基中 |
| `S < seq <= H` | 快照基 + 按序应用 `(S, seq]` 增量，同键后写覆盖前写，区间内未出现的键沿用快照基 |
| `seq > H` | 拒绝，返回 `ErrSeqInFuture` |
| 尚无快照（`S == 0`） | `1 <= seq <= H` 全部按增量扫描 `[1, seq]` 回答 |

“不存在”（键在该位点从未写过）与上述错误不同：它返回 `Result{Exists: false}` 而非报错。
空键（`Write`/`ReadAt`）与空值（`nil` 或长度为 0 的切片）在任何状态变更之前被拒绝，
分别返回 `ErrEmptyKey`、`ErrEmptyValue`。任何一次失败都不改变日志、快照点与快照内容。

### 并发语义

所有方法经同一把 `sync.RWMutex` 串行化对共享状态的访问：快照只在锁内整体替换指针，
读路径在持锁期间基于固定的快照指针与日志切片完成计算，返回值经深拷贝隔离，
因此写入与反复快照可以并发进行，同一实例对同一 `(seq, key)` 的并发读取逐字段相同，
不会读到半成品快照或撕裂的增量应用结果。

### 本地验证：按序号从头重放核对

`ReplayFromZero(seq, key)` 忽略快照、从序号 1 开始重放完整日志，
是独立于双读路径的参照实现（oracle）。测试在每个可读位点上断言
`ReadAt` 与 `ReplayFromZero` 的 `Exists`/`Value` 完全一致，
并用 `go test -race` 覆盖写入、反复快照与读取的并发交叠：

```bash
go test -race -count=1 -v ./snapread
```

`-v` 日志会为每个用例打印输入（键/值）、快照点、读位点、返回值与判定依据
（`basis` 字段形如 `snapshot@5 + incrementals[6,8]`，说明值来自快照基加哪段增量；
重放结果的依据为 `full-replay[1,seq]`）。
