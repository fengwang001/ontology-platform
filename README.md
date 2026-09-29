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

## 带燃料计量的栈式解释器（`stackvm` 包）

`stackvm` 包实现了一个在嵌套调用中按指令与内存扩展扣减燃料、并在帧失败时
回滚存储修改的栈式虚拟机。

### 指令与固定费用

| 指令 | 操作数 | 语义 | 固定费用 |
| --- | --- | --- | --- |
| `PUSH` | `A` | `A` 入栈 | 1 |
| `ADD` | — | 弹出两个栈顶元素，和入栈 | 2 |
| `SLOAD` | — | 弹出 key，读到的值入栈 | 5 |
| `SSTORE` | — | 弹出 value 再弹出 key（栈序 `key, value`），写入共享存储 | 10 |
| `MEXPAND` | `A` | 内存高水位抬到至少 `A` 个字（只增不减） | 1 |
| `CALL` | `A`=请求燃料, `TargetName` | 调用子程序；成功压 1、失败压 0 | 7 |
| `RETURN` | — | 当前栈作为输出成功返回 | 1 |
| `FAIL` | — | 主动失败 | 3 |

指令费用在栈操作与指令语义之前扣减（`FAIL` 的费用也照常扣除）。

### 内存扩展计价

内存以字为单位、只增不减。把内存扩展到 `n` 字的累计成本为

```
cost(n) = 3n + n²/512        （整数除法，平方按 128 位中间结果计算）
```

每次 `MEXPAND` 只收取新老高水位累计成本之差
`cost(新高水位) - cost(老高水位)`，外加该指令的固定费用 1；不抬升高水位时
只收固定费用。

### 调用转发规则

执行 `CALL` 时：

1. 先扣调用指令自身费用 7；
2. 设扣后父帧剩余燃料为 `r`，向子帧转发
   `min(请求量, r - r/64)`；
   - 例如 `r = 64k + 63` 时转发额恰为 `r - r/64`（边界值由
     `TestForwardBoundary` 覆盖）；
3. 子帧燃料耗尽（`fuel exhausted`）时，**转发量全部消耗，不返还**，
   父帧继续执行并收到失败标记 0；
4. 子帧成功，或因栈下溢/主动失败等其他原因失败时，其剩余燃料**返还**
   父帧，父帧继续执行（成功标记 1 / 失败标记 0）；
5. 调用深度超过 `maxDepth` 时，子帧视为立即失败（原因
   `call depth exceeded`）：调用费 7 照扣，但**不转发任何燃料**，
   父帧收到失败标记并继续。

### 静态检查（整体拒绝）

提交程序时对全部程序做静态检查，下列任一错误都会拒绝整个批次，不注册、
不执行任何程序（`StaticError`，原因可区分）：

- 未知指令（`unknown opcode`）；
- 调用目标不存在（`call target does not exist`）；
- `MEXPAND` 的字数为负（`memory expansion word count is negative`）。

### 运行期失败原因与回滚范围

运行期失败按可区分原因（`FailCause`）记在**所在帧**的账目上：

- `fuel exhausted`：燃料耗尽，本帧剩余燃料清零；
- `stack underflow`：栈下溢；
- `explicit fail`：执行了 `FAIL`；
- `call depth exceeded`：调用深度超限（记在那个“没有真正启动”的子帧上）。

每个帧维护自己的存储撤销日志，子孙帧的写入会并入调用它的父帧日志。
任一帧失败时，只撤销**该帧及其全部子孙**的存储修改：

- 子帧失败 → 子帧与它的子孙的写入全部撤销，父帧继续，父帧此前/此后的
  写入保留（`TestGrandchildSuccessChildFails`、
  `TestChildExhaustedParentWriteSurvives`）；
- 顶层帧失败 → 整次执行的全部修改撤销
  （`TestTopLevelFailureRollsBackAll`、`TestRootFuelExhausted`）。

### 燃料守恒式

每次执行的回执 `Receipt` 带逐帧账目 `Frames`（`Initial/Spent/Returned/Cause`）。
`Spent` 只统计该帧自身的指令费与内存扩展费（不含转发给子帧的部分），恒有

```
初始燃料 = Σ 各帧自身消耗(Spent) + 顶层帧剩余(Returned)
```

引擎在每次执行末尾内置断言校验该式，破坏即 panic。对单个非耗尽帧也有
`Initial = Spent + Returned`；燃料耗尽帧 `Spent = Initial`、`Returned = 0`。

### 并发与确定性

- 多个程序可并发提交到同一 `Storage`：整次执行全程持有存储互斥锁，
  彼此串行生效；`Snapshot()` 取同一把锁，读者只能看到某次执行**前或后**
  的完整存储（`TestConcurrentSerializability` 在竞态检测下验证无丢失更新）。
- 相同输入反复执行的输出、逐帧燃料账目与存储结果完全相同
  （`TestDeterminism`）。

### 本地验证

```bash
# 全量测试（用例日志会打印输入、输出与判定依据）
go test -v ./stackvm/

# 竞态检测
go test -race ./...

# 代码检查
gofmt -l .
go vet ./...
```
