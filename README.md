# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 带依赖排序的事务日志回放器（`txreplay`）

`txreplay` 包提供一个可并发使用的事务日志回放器：登记带依赖的事务，依赖全部就绪后自动激活，并按确定顺序回放。

### 暂存与激活

- 事务以正整数标识，用 `Register(id, deps...)` 登记并声明依赖集合（依赖自动去重）。
- 依赖尚未全部就绪的事务进入**暂存集**（`Pending()`）：依赖未登记、或已登记但尚未回放，都会暂存。
- 每当一个事务回放完成，以它为依赖的暂存事务计数递减；剩余未回放依赖数降为 0 时事务**自动激活**，进入就绪集（`Ready()`），无需手动触发。
- 无依赖事务登记后立即激活；`Replay()` 之后再登记的后继事务也能正常激活，回放可增量进行。

### 回放规则：依赖先行 + 标识最小

- `Replay()` 反复选取当前**可回放事务中标识最小者**回放并标记为已回放，直到没有可回放事务，返回本次新回放的标识序列。
- 因此回放序列恒为依赖图的一个拓扑序（依赖一定先于后继），且同一组登记与依赖关系下，每次运行得到的序列完全相同，可复现。
- `Replayed()` 按回放顺序返回已回放集合；`Pending()`/`Ready()` 按标识升序返回，所有查询均可与登记、回放并发调用（内部用读写锁保护）。

### 拒绝原因与原子性

下列非法登记被整体拒绝，可用 `errors.Is` 区分原因；失败时依赖图、暂存集、就绪集与已回放集均不改变：

- `ErrInvalidID`：标识或依赖标识为空/非正整数
- `ErrSelfDependency`：事务把自身声明为依赖
- `ErrDuplicateRegistration`：同一标识重复登记
- `ErrCycleDetected`：新依赖会使依赖图成环（在已登记子图中检测到通向新事务的路径）
- `ErrTooManyTransactions`：登记数量超过上限（默认 `DefaultMaxTransactions`，可用 `WithMaxTransactions` 调整）

```go
r := txreplay.New()
err := r.Register(3, 1) // 3 依赖 1，1 尚未登记 => 暂存
if errors.Is(err, txreplay.ErrCycleDetected) {
    // ...
}
_ = r.Register(1)       // 1 无依赖 => 立即激活，3 仍等待 1 回放
seq := r.Replay()       // [1 3]
```

### 本地验证：用拓扑序核对回放结果

包内提供 `IsTopologicalOrder(order, deps)`，校验序列中每个事务都排在其全部依赖之后。测试与自查均可使用：

```bash
# 详细输出可看到每次登记、依赖、回放序列与判定依据
go test -race -v ./txreplay
```

测试覆盖：暂存后自动激活、依赖先行、标识升序决胜、依赖成环拒绝、五类错误可区分、拒绝原子性、20 次回放序列一致、200 事务并发登记与并发状态查询（`-race`）。

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
