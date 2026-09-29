# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## replay：带依赖排序的事务日志回放器

`replay` 包登记带依赖的事务，并在依赖全部就绪后按确定顺序回放。

### 暂存与激活

- 事务用正整数标识，登记时声明依赖集合：`Register(id, deps)`。
- 登记时若依赖中存在未登记的事务，该事务进入**暂存集**，不参与回放。
- 每当新事务登记成功，暂存集中依赖已全部登记的事务**自动激活**，立即可参与回放。
- `Pending()` / `IsPending(id)` 查询暂存状态，`Replayed()` / `IsReplayed(id)` 查询已回放状态，均可并发调用。

### 回放规则

- `Replay()` 反复在「已激活、未回放、且依赖全部已回放」的事务中选取**标识最小者**回放，直到无可用事务，返回本次回放序列。
- 依赖先行：任何事务的所有依赖都先于它出现在回放序列中（拓扑序）。
- 确定性：同一组登记与依赖关系下，回放序列逐次相同、可复现。
- 并发安全：所有方法由读写锁保护，并发登记互不冲突。

### 失败语义

以下情况整体拒绝登记，且一次失败不改变依赖图、暂存集与已回放集，原因可用 `errors.Is` 区分：

| 原因 | 哨兵错误 |
| --- | --- |
| 非法或空标识（id 或依赖非正整数） | `replay.ErrInvalidID` |
| 自依赖 | `replay.ErrSelfDependency` |
| 重复登记 | `replay.ErrDuplicate` |
| 依赖成环（含经由暂存事务的间接环） | `replay.ErrCycle` |
| 超出事务数上限（`NewReplayer(max)`） | `replay.ErrCapacityExceeded` |

### 本地验证：用拓扑序核对回放结果

1. 运行 `go test -race -v ./replay/`，日志会打印每次登记的 `id` 与 `deps`、回放序列及判定依据。
2. 核对依赖先行：对回放序列中每个事务，其全部依赖在序列中的位置都先于它（测试中的 `assertTopoValid` 即按此判定）。
3. 核对标识最小：把每轮「依赖已全部回放」的事务集合列出，序列中该轮被选中的必须是集合内最小标识。
4. 核对可复现：用相同登记重建回放器再次 `Replay()`，序列应逐次相同（见 `TestDeterministicReplay`）。

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
