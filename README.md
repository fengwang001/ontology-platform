# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 流水线区域故障恢复规划器（`pipeline` 包）

`pipeline.New(taskIDs, edges)` 构建作业图并校验；之后可并发调用完成、失败、
结果丢失上报与各类查询。

### 区域划分

- 边分两种：`pipeline.Pipeline`（流水线）与 `pipeline.Blocking`（阻塞）。
- 忽略边的方向，仅由流水线边连通的任务构成一个区域；区域编号取区域内最小任务编号。
- 阻塞边只在区域之间传递结果，不参与区域连通。

建图时按以下固定顺序整体拒绝（多因并存只报第一个可区分错误）：

1. 边引用不存在的任务（`ErrUnknownTaskEndpoint`）；
2. 重复边，即同一有序任务对 `(From, To)` 出现多次（`ErrDuplicateEdge`；反向对不算重复）；
3. 阻塞边两端落在同一区域（`ErrIntraRegionBlock`）；
4. 区域间依赖图（以阻塞边为有向边）成环（`ErrRegionCycle`）。

### 阻塞结果可用性

一条阻塞边的结果当且仅当“生产者任务已完成”且“未被上报丢失”时可用：

- 所有任务初始为运行中；`Complete(task)` 使其变为已完成，仅对运行中任务有效。
- `LoseResult(from, to)` 要求该有序对为阻塞边且生产者已完成；已丢失再报视为成功且无变化。
- 失败上报应用重启后，重启区域所产阻塞结果的丢失标记被清除；任务重新完成后结果恢复可用。

不符输入按序区分为：`ErrTaskNotFound`、`ErrTaskNotRunning`、`ErrEdgeNotFound`、
`ErrNonBlockingEdge`、`ErrResultNotProduced`。被拒绝的操作不改变任何状态。

### 重启集合三条规则

`Fail(task)` 从失败任务所在区域出发，按下列规则迭代至集合不再变化：

1. 失败任务所在区域必须在集合内；
2. 集合内区域产出的阻塞结果，其全部消费区域也必须在集合内（下游连带重启）；
3. 集合内区域消费的某阻塞结果不可用，且其生产者已完成（即结果被上报丢失）时，
   生产者所在区域也加入集合；生产者仍在运行（结果从未产出）则不加入。

该闭包是单调迭代的唯一最小不动点，故重启集合最小、不含无关区域，且相同上报序列
必然得到完全相同的结果。上报生效后集合内全部任务回到运行中，返回升序的区域编号与
任务编号（`RestartPlan{Regions, Tasks}`）。只读预演可用 `RestartPlanFor(task)`。

### 并发与日志

- 全部上报与查询由同一把互斥锁保护，效果等价于某个串行顺序；重启集合只依赖此前已生效的上报。
- 默认向 stderr 打印结构化日志（输入、输出、每轮拉入区域及判定依据）；
  可用 `pipeline.WithLogger(w)` 重定向，传 `nil` 关闭。

### 本地验证

```bash
go test ./pipeline -v
go test -race ./...
go vet ./...
gofmt -l .
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
