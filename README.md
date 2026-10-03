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

## 差分指令流原位重建排序器

代码位于根包，入口为 `NewPlanner(maxSize, maxInstr)`、`(*Planner).Plan(n, delta)` 和 `(*Planner).Stats()`。输入指令使用 `Instr{Kind: CopyInstr, Src, Len}` 与 `Instr{Kind: AddInstr, Data}`，返回值中的可执行操作为 `StashOp`、`CopyOp`、`UnstashOp` 和 `AddOp`。

### 执行模型

- 初始缓冲区长度为 `max(n,m)`，前 `n` 字节是旧文件；`m>n` 时新增尾部初始内容不参与 Copy 源读取。
- `Copy(src, len)` 按 `memmove` 语义读取旧文件偏移 `src` 开始的区间，写入该指令按新文件顺序紧排得到的 `dst`。
- `Add(data)` 在所有 Copy 和 Unstash 后执行，因此字面量不会先被覆盖。
- `src == dst` 的 Copy 原地不动，直接丢弃：不生成操作、不建图、不参与图 Copy 统计。

### 建边方向

对两条不同的 Copy A、B，当 A 的源区间 `[src,src+len)` 与 B 的目的区间 `[dst,dst+len)` 相交时添加有向边 A→B，表示 A 必须先于 B。同一条 Copy 自身源、目的区间相交不添加自环。

实现使用坐标扫描和活动区间最小结束端堆枚举交集，只枚举真实相交的指令对；不做所有 Copy 对的区间两两比较。区间比较计数保存在包内字段中，测试可断言其上限为 `O(k·⌈log2(k+1)⌉ + E)`，其中 `E` 为边数。

### 拓扑与破环

操作输出顺序固定为：

1. 全部 Stash，按 `dst` 升序，slot 从 0 开始编号。
2. 非暂存 Copy，按拓扑调度顺序执行。
3. 全部 Unstash，按 `dst` 升序。
4. 全部 Add，按 `dst` 升序。

拓扑调度每轮在所有入度为 0 的未执行 Copy 中选择 `dst` 最小者。没有可执行节点但仍有剩余节点时，在剩余诱导图中重新求强连通分量；只考虑大小至少为 2 的分量，并在这些分量的 Copy 内选择 `(len, dst)` 字典序最小者暂存。暂存节点及其全部出入边从图中移除，然后继续拓扑调度；再次卡住时重新计算受影响的强连通分量。

### 校验与统计

校验按顺序返回第一个错误：

1. `n < 0` 或 `n > MaxSize`：`ErrSize`。
2. Copy 越界、`len < 1`、Add 数据为空、未知指令类型，或指令条数超过 `MaxInstr`：`ErrBadDelta`。
3. 新文件长度 `m > MaxSize`：`ErrSize`。

长度累计使用 `int64`。被拒绝的调用不改变累计统计。每次受理后，`Stats()` 增加受理次数、所有暂存 Copy 的长度总和，以及参与建图的 Copy 数；`src == dst` 的 Copy 不计入参与建图数量。`PlanResult` 同时返回本次暂存字节、被暂存 Copy 数和建出的边数，Add 数据会深拷贝，不与输入共享底层数组。

所有方法通过互斥保护累计状态；计划构建本身不修改共享状态，因此结果等价于某个并发调用的串行交错顺序，并且相同输入产生完全相同的计划。

### 本地验证

标准命令：

```bash
go test ./...
go test -race ./...
go vet ./...
```

查看 2000 组随机指令流的输入、输出、边数、区间比较次数和判定依据：

```bash
go test -run TestRandomIntervalSchedulesAgainstNaive -v
```

随机测试同时执行三种判定：高效建边与逐对比较建边得到相同邻接表，实际调度与“每轮重算全部 SCC”的朴素规则得到相同拓扑与暂存结果，最后把生成的 Stash/Copy/Unstash/Add 操作真实写入缓冲区，与直接按原指令流拼出的新文件逐字节比较。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
