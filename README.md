# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 按水位回收的撤回日志回收器

代码位于 `withdrawlog/` 包（`withdrawlog/reclaimer.go`）。撤回记录按序号从 1
开始单调递增追加；读侧通过「快照」固定一个水位，回收器依据所有活跃快照的水位
安全地回收历史撤回记录。

### 水位与回收规则

- 记录通过 `Append` 追加，返回从 1 开始、严格递增的序号。
- `OpenSnapshot` 在调用瞬间记录水位 `W`（等于当时的当前最大序号）。该快照可以
  重放所有满足 `1 <= seq <= W` 且尚未被回收的记录；打开之后新追加的记录（序号
  大于 `W`）对该快照不可见。快照水位一经打开即固定不变，可多个快照并存。
- `Replay(snapshot, seq)` 按快照水位与回收状态判定，返回记录或错误。
- 回收是**物理删除且永久不可逆**：被回收的位点之后即使在某个更晚打开的快照水位
  之内，也永远无法再重放。

### 回收上界计算

记活跃（已打开未关闭）快照水位集合为 `{w1..wk}`，当前最大序号为 `lastSeq`：

- 存在活跃快照时，回收上界 = `min(w1, ..., wk)`；
- 无活跃快照时，回收上界 = `lastSeq`，即全部回收。

`Reclaim` 删除所有 `seq <= 上界` 的记录，**包含等号**，并把内部水位推进到该
上界。内部水位只增不减（单调不回退）；关闭低水位快照后会立即用存活快照集合
重估最小值，因此上界可能随后变大。`ReclaimUpperBound` 只读返回当前上界。

### 可判定错误

四类互不相同的哨兵错误，用 `errors.Is` 判定；所有失败路径都不写入任何状态，被拒
后可继续正常使用：

- `ErrReplayOutOfRange`：重放位点越界，`seq <= 0` 或 `seq >` 该快照水位；
- `ErrReplayReclaimed`：重放位点 `seq <=` 已回收水位，记录已永久删除；
- `ErrSnapshotNotFound`：关闭或重放一个不存在（或已关闭）的快照；
- `ErrTooManySnapshots`：活跃快照数达到 `New(maxSnapshots)` 设定的上限。

### 并发模型

内部使用 `sync.RWMutex`：`Append` / `OpenSnapshot` / `CloseSnapshot` /
`Reclaim` 持写锁互斥串行；`Replay` 与 `ReclaimUpperBound` 持读锁，可彼此并发、
也可与对方并发执行。因此「并发打开再关闭若干快照」与顺序执行这些操作得到的
回收上界完全一致（每个操作都可线性化到某个串行点）。

### 本地验证

```bash
# 全部测试（详细日志会打印输入、结果与判定依据）
go test -v ./withdrawlog

# 竞态检测（建议多跑几轮）
go test -race -count=3 ./withdrawlog

# 覆盖率
go test -coverprofile=coverage.out ./withdrawlog
go tool cover -func=coverage.out
```

若默认 `GOCACHE` 所在文件系统只读，可临时重定向：

```bash
export GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache
```

测试场景覆盖：回收含等号边界、无活跃快照全收、关闭快照后最小水位重估、
四类非法输入被拒且状态不变/可继续使用、并发快照与顺序参照一致、回收与重放并发。

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
