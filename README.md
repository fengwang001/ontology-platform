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

## 增量中位数（`median` 包）

`median` 包对一个整数**多重集**（允许重复值）支持动态加入、撤回与中位数查询，
查询结果与“把全部元素升序排序后朴素取值”完全一致。

### 中位数定义

- 将多重集升序排列为 `s`，长度为 `n`，中位数取 `s[(n-1)/2]`，即**中间偏下**位置。
- 奇数个取正中间；偶数个取两个中间值中偏下（较小）的一个。
- 空集查询 `Median` 返回 `ErrEmpty`。

### 数据结构与维护规则

- `lo`：保存较小一半的**大顶堆**，堆顶即中位数；`hi`：保存较大一半的**小顶堆**。
- 不变量：`lo.valid == hi.valid`（偶数）或 `lo.valid == hi.valid + 1`（奇数），
  且 `lo` 中每个有效元素不大于 `hi` 中每个有效元素。
- **加入**：新值不大于 `lo` 堆顶则入 `lo`，否则入 `hi`；随后再平衡
  （把超出平衡的堆顶有效值搬到另一侧）。
- **撤回**：采用懒删除。先依据“`lo` 堆顶是否 `>= v`”判定该有效副本在哪一侧，
  在该侧登记待删并把该侧有效计数减一，再清理两堆堆顶的作废副本并再平衡。
  作废副本只有浮到堆顶时才被物理丢弃，埋在堆中的副本继续等待，不影响查询正确性。
- 重复值按副本计数：撤回一次只作废一个副本；即使某值的作废副本仍在堆中等待清理，
  只要当前没有有效副本，再次撤回一律报 `ErrNotFound`。
- 有效总数由 `count map[int]int` 与两侧 `valid` 共同记录；`Check()` 做完整自检
  （计数一致、两侧差不超过一、堆序、堆顶非作废、跨侧大小关系）。

### 原子批处理与错误类别

- `Apply(ops...)` 在一次提交中执行多步。提交前先在“虚拟计数”上**整批预校验**：
  任何一步非法则整批拒绝，两个堆、计数与长度完全不变（失败不留痕）。
- `Add` / `Remove` 是单步批处理的便捷封装。
- 四类互相区分的错误（用 `errors.Is` 判别）：
  - `ErrInvalidArgument`：nil 接收者、空批次、未知操作类型、非正上限等非法参数；
  - `ErrEmpty`：在空多重集上查询中位数；
  - `ErrNotFound`：撤回当前不存在的有效值（即使其作废副本仍在堆中）；
  - `ErrLimitExceeded`：加入会使元素个数超过 `NewWithMax` 配置的上限
    （默认 `median.MaxElements`）。

### 并发

- 所有方法均为并发安全：读路径（`Median` / `Len` / `Check`）使用读锁且**不修改**
  任何内部结构（提交结束时已保证堆顶有效），可被多个执行体并发调用，也可与
  `Add` / `Remove` / `Apply` 提交并发。

### 本地验证

```bash
# 全量测试（含竞态检测与逐步输入/中位数/判定依据日志）
go test -race -v ./median

# 与朴素排序参照的 4000 步随机对照、并发场景
go test -race ./...

# 覆盖率与静态检查
go test -coverprofile=coverage.out ./median
go tool cover -func=coverage.out
gofmt -l .
go vet ./...
```

若默认构建缓存目录只读，可指定缓存目录后再运行，例如：
`GOCACHE=/tmp/gocache go test -race ./...`。
