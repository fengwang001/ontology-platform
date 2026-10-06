# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多卷一致性组快照协调服务（`cg` 包）

`cg/` 实现了可精确复现的多卷一致性组快照协调：卷分组（2–16 卷、每卷至多
属于一个组、空闲才允许成员变更）、快照发起、逐卷冻结确认、冻结期间按到达
次序排队的写入、提交/中止、冻结截止与最长保持两类超时自动中止，以及严格的
拒绝次序（参数非法 → 时钟回退 → 超时检测 → 操作自身判定）。

详细设计见 [`docs/design.md`](docs/design.md)。

```go
co := cg.New(cg.Config{DefaultQueueCapacity: 8, MaxFreezeHold: 5})
co.WithTracer(cg.NewLogTracer(os.Stdout)) // 可选：逐条打印输入/输出/判定依据

_ = co.CreateGroup(0, cg.GroupSpec{GroupID: "g", VolumeIDs: []string{"a", "b"}})
_, _ = co.Write(0, "a", "w1")
id, _ := co.BeginSnapshot(1, "g", 4) // deadline=4
_, _ = co.ConfirmFreeze(2, "g", "a")
_, _ = co.Write(2, "a", "q")         // 已确认卷：排队
_, _ = co.Write(3, "b", "w2")        // 未确认卷：照常应用
_, _ = co.ConfirmFreeze(4, "g", "b") // t==deadline 有效，此刻为快照点
rec, _ := co.Commit(5, "g")          // rec.Point=4，rec.Cutoffs={a:1,b:1}
_ = id
```

模块：

- `cg/coordinator.go` / `cg/coordinator_ops.go`：串行化入口与统一判定次序。
- `cg/group.go` / `cg/volume.go`：组快照状态机与卷序号/冻结队列。
- `cg/errors.go` / `cg/types.go` / `cg/trace.go`：错误类别、值类型、决策日志。
- `cg/cgtest/`：独立编写的朴素逐事件模型与随机差分测试。

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache GOTMPDIR=/tmp   # 只读 HOME 缓存时

go test ./...                    # 含与朴素模型的随机对照
go test -race ./...              # 并发可串行化验证
go test -v ./cg/                 # 全部边界用例
go test -bench=. ./cg/           # 写排队判定 / 确认 的 O(1) 证据
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
