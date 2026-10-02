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

## 两阶段提交 sink 事务名册（`ontology` 包）

`ontology.NewRegistry(P, M)` 构造一个事务名册：`P` 为子任务数（1–64），
`M` 为恢复清扫时的连续缺失容忍（1–1000）。名册维护检查点水位
`lb`（最近一次 Barrier，初值 0）、完成通知水位 `ln`（初值 0）、
生命周期号 `life`（初值 0，每次 Restore 加一）与事务表。

### 事务命名与世代

- 事务名为 `(s, k)`：子任务号 `s` 与检查点号 `k`。子任务 `s` 当前打开
  事务的名字恒为 `(s, lb+1)`，每次 Barrier 后自动换名。
- 事务状态为 `OPEN`、`PREPARED`、`COMMITTED`、`ABORTED`；表项另记录
  世代 `epoch`、记录数 `n` 与创建时的 `life`。
- `Write(s, n)`（`0<=s<P`，`1<=n<=10^6`）：
  - 名下有创建于当前 `life` 的 `OPEN` 事务：`n` 直接累加；
  - 名下有旧 `life` 的 `OPEN`/`PREPARED` 遗留事务：先中止旧事务
    （其记录计入中止），再以「旧世代 + 1」开新事务；
  - 名下无事务或是 `ABORTED`：开新事务，世代为旧世代 + 1，
    从无事务时世代为 0。新事务为 `OPEN`，`life` 取当前值。
- 同一名字下任一时刻至多有一个非终态事务；`COMMITTED` 不可变。

### Barrier 与通知提交（跳号语义）

- `Barrier(cp)` 要求 `cp == lb+1`（否则报检查点乱序）：按 `s` 升序把
  名字 `(s, cp)` 下当前 `life` 的 `OPEN` 事务转为 `PREPARED`，没有写入
  的子任务不产生事务，然后令 `lb=cp`，返回预提交清单。
- `Complete(c)` 提交所有「当前 `life`、`PREPARED`、`k<=c`」的事务，
  按 `(k, s)` 升序返回，并令 `ln=c`。通知允许跳号：`Complete(3)` 会把
  检查点 1、2、3 上尚未提交的事务一并提交。
- `c<=ln` 报通知过期，`c>lb` 报通知超前（互斥）。旧 `life` 遗留的
  事务从不被提交。

### 恢复三步：提交、清扫、换代

`Restore(c, P')` 原子完成三步（`P'` 为新并行度，1–64）：

1. **提交**：提交当前 `life`、`PREPARED`、`k<=c` 的事务（按 `(k,s)`
   升序），包含 `k>ln` 者——检查点已完成而通知尚未送达也会提交；
   `c<ln` 报恢复回退，`c>lb` 报恢复超前（互斥）。
2. **清扫**：`s` 从 0 扫到 `max(P, P')-1`（缩容扫到旧并行度，扩容扫
   到新并行度），每个 `s` 从 `(s, c+1)` 起依次探测。每探测一个名字
   探测数加一：遇 `OPEN`/`PREPARED`（任意 `life`）即中止并把连续缺失
   计数清零；无事务或已是终态则缺失数加一；连续缺失达到 `M` 时该
   子任务清扫结束。清扫不碰 `c` 及以前的名字。
3. **换代**：令 `P=P'`、`lb=ln=c`、`life` 加一。

返回提交清单、按清扫次序的中止清单与本次探测次数。

**缺失容忍的含义与局限**：`M` 只保证扫出从 `c+1` 起每个子任务窗口内
的悬挂事务；一旦连续遇到 `M` 个缺失名字就停止，窗口之外的事务（例如
`M=1` 时 `c+2` 处的 OPEN 事务）会遗留。遗留事务不会被后来的
`Complete` 提交（旧 `life` 过滤），但会在后续 `Write` 落到同一名字时
被先中止再重开，或在下一次 Restore 的窗口中被扫到。

### 查询、计数与记录守恒

- `Txn(s, k)` 返回状态、世代、记录数；不存在报 `ErrNoSuchTxn`。
- `Pending()` 返回全部 `OPEN`/`PREPARED` 名字，按 `(s,k)` 升序。
- `Stats()` 返回已提交、已中止、`OPEN`、`PREPARED` 的记录总数、
  探测累计数 `ProbeCount` 与 `visited`。
- `visited` 统计 Complete 与恢复提交时被访问的待提交条目数，每次不
  超过本次提交事务数 + 1，且与历史已提交事务数量无关（提交索引只
  保留未提交前缀）；`TestVisitedScaleIndependent` 在 10 与 100000
  个已提交检查点两档下验证增量均为 1。
- 任何时刻写入记录总数满足守恒式：

```text
全部成功 Write 的 n 之和 = committedN + abortedN + openN + preparedN
```

所有操作互斥（`sync.Mutex`），Barrier、Complete、Restore 各自是
不可分割的原子步骤；被拒绝的操作不改变任何状态。相同操作序列重放
得到完全相同的清单、事务表与计数。

### 本地验证

```bash
go test ./ontology -v            # 规则示例、边界、清扫、两档计数、并发等用例
go test -race ./...              # 竞态检测（含并发原子性用例）
go test -cover ./...             # 语句覆盖 100%
go test -run TestDifferentialRandom -v ./ontology
# 2000 组随机操作序列与朴素模拟器逐步对照，
# 日志打印每步输入、真实/模拟双方输出与判定依据（事务表逐字段、守恒式）
```
