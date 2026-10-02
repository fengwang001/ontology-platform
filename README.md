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

`epaxos` 实现 EPaxos 风格的已提交实例执行排序器。实例以 `(副本 r, 槽位 i)`
标识（`0 <= r < N`，`i >= 1`，`N` 与未执行已提交实例数上限 `Cap` 在
`NewExecutor(N, Cap)` 时给出，均须为正整数）。

### 接口

- `Commit(inst, seq, deps)`：登记已提交实例，`seq` 为正整数，`deps` 顺序无意义。
- `Execute()`：对全部已提交未执行实例求依赖图并返回本次新执行实例的有序列表。
- `Pending()`：返回已提交未执行实例按 `(r, i)` 升序的列表。

所有方法可并发调用，结果等价于某个串行顺序；`Execute` 返回新分配的切片，
不与内部状态别名；相同操作序列重放得到完全相同的序列与错误。

### 可执行条件

依赖图中边 `u -> d` 表示 `u` 依赖 `d`。依赖已执行的实例视为已满足、不构成
边；依赖尚未提交的实例使 `u` 被阻塞。实例可执行当且仅当沿依赖边可达的全部
实例（已执行的不再展开）都已提交；被阻塞者留待后续 `Execute`。

### 排序规则

- 可执行实例按强连通分量（SCC）划分；分量之间被依赖者的分量先输出。
- 在已就绪（其外部依赖的分量均已输出）的分量中，取各分量成员按
  `(seq 升序, r 升序, i 升序)` 排列后的首个成员，首个成员最小者先输出。
- 分量内部成员按 `(seq, r, i)` 升序输出。

因此同一已提交集合无论 `Commit` 先后次序，一次 `Execute` 的序列相同。

### 错误与优先级

`Commit` 按以下固定顺序检查，只报告第一个错误（均为可 `errors.Is` 区分的
哨兵错误），被拒绝的操作不改变任何状态：

1. `ErrInvalidInstance`：实例或某个依赖的 `r` 越界或 `i < 1`；
2. `ErrInvalidSeq`：`seq` 非正；
3. `ErrSelfDependency`：依赖含自身；
4. `ErrDuplicateDep`：依赖含重复；
5. `ErrConflict`：同一实例已登记且 `seq` 或依赖集合（忽略顺序）不同；
6. `ErrCapExceeded`：登记新实例时未执行已提交实例数已达 `Cap`。

`seq` 与依赖集合都相同的重复登记（无论是否已执行）是合法空操作；已执行实例
的 `seq` 与依赖集合仍被记住用于冲突判定。构造时 `N` 或 `Cap` 非正返回
`ErrInvalidParams`。

### 本地验证

```bash
# 全部单测（含随机依赖图与朴素可达闭包实现对拍，日志打印输入/输出/判定依据）
go test -v ./epaxos/

# 竞态检测
go test -race ./epaxos/

# 仅对拍用例
go test -v -run TestFuzzAgainstNaive ./epaxos/
```
