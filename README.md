# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 控制流图活跃变量分析（`liveness` 包）

支持增量录入基本块，封口后按后向数据流求出每块入口/出口活跃变量集合，
结果为最小不动点且对相同录入序列可逐位复现。

### 基本概念

- 每条指令含使用集 `Uses` 与定义集 `Defs`；**同一条指令内先使用后定义**，
  因此 `x = x + 1` 中 `x` 既是使用也是定义，且该使用向上行暴露。
- `UE(B)`（upward-exposed uses，上行暴露使用）：变量在**本块首次定义之前**
  就被使用（按指令顺序、同指令先 Uses 后 Defs 扫描）。
- `Def(B)`：本块内被定义过的全部变量。

### 数据流方程（后向）

```
LiveOut(B) = ∪_{S ∈ successors(B)} LiveIn(S)   // 无后继时为空集
LiveIn(B)  = UE(B) ∪ (LiveOut(B) − Def(B))
```

所有集合以空集为初值，按块编号升序逐轮重算，直到某轮无任何变化。
该传递函数对集合包含关系单调，从空集出发的极限即**最小不动点**；
固定的迭代顺序使相同输入的重放结果完全一致（变量一律按字典序输出）。

入口块（第一个成功录入的块）的 `LiveIn` 即「可能使用未定义变量」的集合，
可用 `EntryLiveIn()` 单独查询。

### 录入与封口规则

- `AddBlock(id, instructions, successors)`：编号为非负整数；后继允许
  **前向引用**尚未录入的块，封口时才校验引用。
- `Seal()`：校验后继全部存在并计算不动点；封口成功后拒绝再添加块。
  封口失败不改动已录入数据，可继续补块后再次封口。
- 查询（`Block` / `Blocks` / `EntryID` / `EntryLiveIn`）仅在封口后可用，
  返回的快照均为拷贝，外部修改不影响分析器。

错误按固定优先级只报第一个，`AnalysisError.Code` 可程序化区分：

| 操作 | 优先级（从先到后） |
| --- | --- |
| 添加 | `negative_block_id` → `duplicate_block_id` → `already_sealed` |
| 封口 | `sealed_twice` → `no_blocks` → `missing_successor` |
| 查询 | `not_sealed` → `no_such_block` |

`missing_successor` 的定位规则：按引用方块编号升序、同一块内按后继出现序，
取第一处（`Detail` 形如 `block 0 references missing successor 4`）。

### 并发语义

所有方法在同一把互斥锁下串行化，并发调用的结果等价于某个串行顺序；
封口成功后内部结果不可变，封口后的查询永不改变。

### 本地验证

```bash
# 全量测试（含与朴素逐轮迭代的逐项对照、300 个固定种子随机 CFG）
go test -v ./liveness

# 竞态检测（并发录入/封口/查询用例）
go test -race -v ./liveness

# 复现性：多次运行结果与日志完全一致
go test -run TestDeterministicReplay -v ./liveness
go test -run TestRandomizedAgainstNaive -v ./liveness
```

测试日志打印每个用例的输入（块、后继、指令 uses/defs）、输出
（`UE/Def/LiveIn/LiveOut`）以及判定依据。`go test -v` 即可查看。

### 最小用法

```go
a := liveness.NewAnalyzer()
_ = a.AddBlock(0, []liveness.Instruction{{Uses: []string{"x"}, Defs: []string{"x"}}}, nil)
if err := a.Seal(); err != nil {
    log.Fatal(err)
}
snap, _ := a.Block(0)           // UE=[x] Def=[x] LiveIn=[x] LiveOut=[]
undefined, _ := a.EntryLiveIn() // [x]
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
