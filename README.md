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

## EPaxos 执行排序器（`epaxos` 包）

`epaxos` 实现 EPaxos 风格的已提交实例执行排序器。实例以（副本 `R`，槽位 `I`）
标识，`R ∈ [0, N)`、`I ≥ 1`；`N` 与未执行已提交实例数上限 `Cap` 在 `New(N, Cap)`
时给出，均须为正整数，否则返回 `ErrInvalidConfig`。

### 接口

- `Commit(inst, seq, deps)`：登记已提交实例，`seq` 为正整数，`deps` 顺序无意义。
- `Execute()`：对全部已提交未执行实例求依赖图并执行可执行者，返回本次新执行
  实例的有序列表（新分配的切片，不与内部状态别名）。
- `Pending()`：返回已提交未执行实例按 `(R, I)` 升序的列表。

所有方法可并发调用，结果等价于某个串行顺序；已执行实例的 `seq` 与依赖集合
仍被记住用于冲突判定。

### 可执行条件

边 `u → d` 表示 `u` 依赖 `d`。依赖已执行的实例视为已满足、不构成边；依赖尚未
提交的实例使 `u` 被阻塞，且沿依赖边可达 `u` 的实例（传递依赖者）同样被阻塞。
一个实例可执行当且仅当沿依赖边可达的全部实例都已提交。被阻塞者留待其缺失的
依赖提交后的下一次 `Execute` 释放。

### 执行排序规则

- 可执行实例按依赖图的强连通分量（SCC）划分。
- 分量之间：被依赖者先输出。在已就绪（其依赖的分量均已输出）的分量中，取各
  分量成员按 `(seq, R, I)` 升序排列后的首个成员，首个成员最小者所在分量先输出。
- 分量内部：成员按 `(seq 升序, R 升序, I 升序)` 输出。

因此对同一已提交集合，无论 `Commit` 先后次序，一次 `Execute` 得到的序列相同；
相同操作序列重放得到完全相同的序列与错误。

### Commit 错误优先级

`Commit` 只报告按以下顺序检查到的第一个错误，且被拒绝的操作不改变任何状态：

1. `ErrInvalidInstance`：实例或某个依赖的 `R` 越界或 `I < 1`（先查实例本身，
   再按给定顺序查依赖）。
2. `ErrInvalidSeq`：`seq` 非正。
3. `ErrSelfDependency`：依赖含自身。
4. `ErrDuplicateDependency`：依赖含重复。
5. `ErrConflictingCommit`：同一实例已登记且 `seq` 或依赖集合（忽略顺序）不同。
   `seq` 与依赖集合都相同的重复登记（无论是否已执行）是合法空操作。
6. `ErrCapacityExceeded`：登记新实例时未执行已提交实例数已达 `Cap`。

### 本地验证

```bash
# 全部测试（含与可达闭包朴素实现对拍的随机测试）
go test ./epaxos/

# 竞态检测
go test -race ./epaxos/

# 查看对拍日志（输入、输出与判定依据：阻塞集合与分量划分）
go test -v -run TestExecuteMatchesNaiveReference ./epaxos/
```
