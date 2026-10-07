# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 子系统：嵌套动作事务与校验钩子

本仓库当前交付「嵌套动作事务与校验钩子触发顺序」子系统：
动作在其事务范围内可调用另一个（或同一）动作定义，两层动作各自
注册的校验钩子的触发范围、可见快照与短路规则精确且一致。
设计取舍、被放弃的方案与 O(1) 快照开销证明见 [docs/design.md](docs/design.md)。

### 包结构

- `spec` — 共享数据模型（对象模式、写入、动作定义）
- `errs` — 错误归一化（参数非法 / 前置钩子失败 / 后置钩子聚合失败）
- `hooks` — 钩子注册与触发调度、只读状态视图、触发记录
- `tx` — 事务边界管理（工作副本、写入校验、O(1) 视图、提交 / 回退）
- `engine` — 动作执行引擎与基于序号的并发串行化调度
- `naive` — 独立实现的朴素对照模型（测试 oracle）
- `cmd/server` — 演示入口

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

# 随机嵌套动作序列与朴素模型的差分对照（打印输入、实际输出与判定依据）
go test -v ./engine/ -run TestRandomNestedSequencesMatchNaive

# O(1) 快照开销证明与基准
go test -v ./engine/ -run TestSnapshotCostIndependentOfDepthAndWrites
go test ./engine/ -run '^$' -bench BenchmarkViewConstruction

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
