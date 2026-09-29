# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

若环境中 `go` 不在 PATH，或默认构建缓存目录只读，可使用：

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache
```

## 物化视图依赖图（`viewgraph` 包）

`viewgraph` 实现了带依赖的物化视图 DAG：基视图（base view）由外部
`Set` 设值，派生视图只能由纯函数 `ComputeFn` 从其依赖当前值推导。
同一输入必须产生同一输出（可复现）。

### 注册与拓扑序

- `RegisterBase(name)` 注册基视图；`RegisterView(name, deps, fn)` 注册
  派生视图。依赖名在注册时允许指向尚未注册的视图（前向引用），后注册的
  视图会自动补全反向依赖边。
- 拓扑序由 Kahn 算法生成，依赖恒先于被依赖者；同层并列时按注册先后
  打破平局，因此顺序完全确定（`TopoOrder` 可查）。
- “依赖未注册”和“依赖成环”都在需要拓扑序时（`Recompute` /
  `TopoOrder` / `Verify` / `FullRecompute`）才判定，错误原因分别为
  `ErrDependencyNotFound`、`ErrCycle`，环上的节点会列在错误信息中。
- 注册期错误彼此可区分且整体拒绝：`ErrEmptyName`（空视图名或空依赖名）、
  `ErrDuplicateName`（重名）、`ErrNilCompute`（计算函数为 nil）。

### 失效传播与重算

- `Set(name, value)` 只允许作用于基视图，写入后该基视图与它的整个下游
  传递闭包都被标记为脏（脏标记是幂等的集合，重复设值不会累积多轮）。
- `Recompute` 按拓扑序遍历，且只求值脏的派生视图：
  - 同一轮内被多次失效的视图至多求值一次；
  - 未标记为脏的视图绝不求值（出现在报告的 `Skipped` 中）；
  - 求值结果先全部暂存，全部成功后一次性提交，期间基视图的脏位也在提交
    点一并清除；任何失败（依赖无值、计算函数报错、环、依赖缺失）都不改动
    值与脏标记，读者继续观察上一次完整重算的状态。
- 未设值的基视图参与重算时返回 `ErrDependencyNotSet`。
- 读写未注册名返回 `ErrViewNotFound`；对派生视图 `Set` 返回
  `ErrNotBaseView`；读取尚不一致（脏）的视图返回 `ErrViewNotSet`，
  避免暴露与其输入不一致的值。

### 并发与一致性

所有操作经同一把 `sync.RWMutex` 保护：`Get` / `Snapshot` / `Verify` /
`FullRecompute` 使用读锁可彼此并发；`Set` 与 `Recompute` 串行化发布。
`Snapshot` 只包含干净视图，因此并发读者拿到的值集合必然属于某次完整
重算之后的状态，不可能读到半轮混合的中间值。

### 自检与全量重算对照

- `Verify()`：只读校验不变量——每个干净派生视图的存储值等于其
  `ComputeFn` 作用于依赖当前值的结果，发现首个不一致即报错。
- `FullRecompute()`：不修改任何状态，从全部基视图出发按拓扑序对每个
  派生视图恰好求值一次，作为独立的“全量重算 oracle”。

本地验证方法（增量结果必须始终等于全量 oracle）：

```bash
go test -race -v ./viewgraph

# 用例 TestIncrementalMatchesFullRecompute 在每一轮增量 Recompute 后
# 逐视图对照 FullRecompute；TestConcurrentConsistency 在 Set+Recompute
# 持续进行时校验每个快照都对应某个完整轮次，且终态与全量重算一致。
```

测试日志（`go test -v`）会打印每个用例的注册顺序、设值后与重算后的
各视图值、脏标记以及对应的判定依据。

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
