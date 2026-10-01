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

## 控制流图活跃变量分析（`liveness` 包）

`liveness` 包实现增量录入基本块、封口后按后向数据流方程求每块入口/出口活跃变量集合，结果为最小不动点。

### 概念定义

- 指令 `Instruction{Uses, Defs}`：仅描述使用变量与定义变量；**同一条指令内先使用后定义**，所以 `x = x + 1` 表示使用 `x` 再定义 `x`。
- 块级摘要：
  - **上行暴露使用集 UE（Upward Exposed Uses）**：按指令顺序扫描，变量在本块被定义**之前**就被使用则进入 UE（同一指令中 Uses 先于 Defs 生效）。
  - **定义集 Def**：块内任意指令定义过的全部变量。
- 后向数据流方程（对每块 B）：
  - `LiveOut(B) = ∪ LiveIn(S)`，S 取遍 B 的后继；无后继时 `LiveOut = ∅`
  - `LiveIn(B) = UE(B) ∪ (LiveOut(B) − Def(B))`
- 从空集开始迭代、集合单调增长直到一轮内无变化，所得即**最小不动点**（等价于经典的逐轮朴素迭代，测试中以独立的 `naiveSolve` 逐轮重算做对照）。
- 入口块 = 第一个成功添加的块；其 `LiveIn` 即「可能被使用但未定义」的变量集合，可用 `EntryLiveIn()` 单独查询。

### 封口规则与错误

- 后继允许前向引用尚未添加的块；只在 `Seal()` 时统一校验。
- 校验顺序（只报第一个错误，哨兵错误可用 `errors.Is` 区分）：
  - 添加 `AddBlock`：`ErrNegativeBlockID`（编号为负）→ `ErrDuplicateBlockID`（编号已存在）→ `ErrAlreadySealed`（封口后添加）。
  - 封口 `Seal`：`ErrSealedTwice`（重复封口）→ `ErrNoBlocks`（没有任何块）→ `ErrMissingSucc`（后继不存在）。后继不存在按**块编号升序**遍历块、块内按后继出现序报第一处，错误为 `*MissingSuccessorError{BlockID, Successor}`。
  - 查询：先报 `ErrNotSealed`（尚未封口），再报 `ErrNoSuchBlock`（块不存在）。
- 任何被拒绝的操作都不改变已录入数据；封口失败（如缺后继）后可继续 `AddBlock` 再 `Seal`。封口成功后结果不可变，查询结果永不改变。

### 并发与确定性

- 添加、封口、查询内部由读写锁串行化，并发调用的结果等价于某个合法的串行顺序；封口最多成功一次。
- 所有变量集合按字典序输出，块按添加顺序输出；相同录入序列重放得到完全相同的结果。

### 使用示例

```go
a := liveness.NewAnalyzer()
_ = a.AddBlock(liveness.BlockSpec{
    ID: 0,
    Instructions: []liveness.Instruction{
        {Uses: []string{"x"}, Defs: []string{"x"}}, // x = x + 1，先用后定义
    },
    Successors: []int{1}, // 允许前向引用
})
_ = a.AddBlock(liveness.BlockSpec{ID: 1, Instructions: []liveness.Instruction{{Uses: []string{"y"}}}})
if err := a.Seal(); err != nil { /* 处理校验错误 */ }
in, _ := a.LiveIn(0)        // [x]
out, _ := a.LiveOut(0)      // [y]
entry, _ := a.EntryLiveIn() // 可能使用未定义变量的集合
all, _ := a.Results()       // 全部块的 UE/Def/LiveIn/LiveOut，字典序
```

### 本地验证

```bash
# 全量测试（含 -race 并发检测；-v 打印输入、输出与判定依据日志）
go test -race -v ./liveness/

# 重放确定性与环路最小不动点对照
go test -run 'TestReplayDeterminism|TestLoopConvergence' -v ./liveness/
```

覆盖场景：同一指令先用后定义、块内先定义后使用不上行暴露、多后继取并、环路收敛、只在一条分支上定义的变量在汇合处仍活跃、入口块使用未定义变量、前向引用后补块、错误优先级、封口失败后重试、并发 add/seal/query 与重放确定性。
