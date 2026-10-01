# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 字节码分支松弛汇编器

`assembler` 包提供线程安全的增量汇编器：

- `AppendPad(n)`：追加 `PAD`，`n` 必须满足 `1 <= n <= 1000`，占 `n` 字节。
- `AppendJump(assembler.JMP, label)` / `AppendJump(assembler.JZ, label)`：追加跳转。
- `DefineLabel(name)`：把标签绑定到当前将追加的下一条指令的起始地址；在末尾调用时绑定最终布局末尾。
- `Assemble()`：不修改汇编器状态，返回每条指令的 `Start`、`Size`、`Form`、`Offset`、`Target`，以及标签地址和 `TotalLength`。
- `Snapshot()` / `Len()`：查询当前操作序列和标签绑定的防御性副本。

跳转长度固定为：

| 指令 | 短形式 | 长形式 |
| --- | ---: | ---: |
| `JMP` | 2 字节 | 5 字节 |
| `JZ` | 2 字节 | 6 字节 |

偏移定义为：

```text
offset = target_address - (jump_start + jump_size)
```

也就是目标地址减去该跳转自身末尾地址（下一条指令的起始地址）。短形式只接受闭区间 `[-128, 127]`。

### 松弛迭代规则

1. 第一轮布局中所有跳转先取短形式。
2. 用当前形式完整重排地址并计算所有偏移。
3. 同时扫描本轮布局：所有当前为短形式且偏移越界的跳转，都在下一轮改成长形式。
4. 使用下一轮形式重新布局，直到某一轮没有任何变化。
5. 形式只增不缩；即使某次重排后偏移重新落入短跳转范围，已经变长的跳转仍保持长形式。

因此结果是从“全短形式”出发、单调扩张形式集合得到的最小不动点。`Rounds` 记录发生形式扩张的轮数；同一轮发现的多处越界不会串行提前影响彼此。汇编失败只返回错误，不保存半成品布局。

### 错误模型与优先级

错误可通过 `errors.Is` 区分，并可用 `errors.As` 取得详细字段：

- `ErrInvalidPadSize` / `InvalidPadSizeError{Size}`：PAD 大小不在 `[1,1000]`。
- `ErrEmptyLabelName` / `EmptyLabelNameError`：标签名为空。
- `ErrLabelAlreadyDefined` / `LabelAlreadyDefinedError{Name}`：标签重复定义。
- `ErrUndefinedLabel` / `UndefinedLabelError{OperationIndex, Label}`：汇编时引用了未定义标签。

定义标签时如果名字同时为空且与已有名字冲突，优先报告空名字。汇编发现多个未定义标签时，报告操作下标最小的一处。任何被拒绝的 `AppendPad`、`AppendJump` 或 `DefineLabel` 都不会改变既有操作序列与标签表。

### 并发与确定性

`Assembler` 内部使用读写锁，追加、定义标签、汇编和查询并发调用时等价于某个合法串行顺序。`Assemble` 是纯计算：同一序列重复汇编结果完全相同；相同操作序列重放也得到相同的地址、形式、偏移、轮数和总长度。

### 本地验证

```bash
# 详细查看边界输入、输出与判定依据
GOCACHE=/tmp/ontologies-gocache go test -race -v ./assembler

# 全量验证
GOCACHE=/tmp/ontologies-gocache go test ./...
go vet ./...
gofmt -l .
```

测试中的朴素参照器独立按“全短形式开始、逐轮同时扩张、重新布局”的规则实现，用于逐项对照真实结果。覆盖前向 `127/128`、后向 `-128/-129` 判定边界、变长连锁、两条跳转同轮越界、末尾标签和跳到自身等场景。

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
