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

## 大事务溢写与按序回放（`spill` 包）

`spill` 包（`spill/spill.go`）在内存行数有上限的前提下缓冲多个并发事务
的行，提交时按追加顺序回放，使下游只看到已提交事务且行序可复现。

### 溢写规则

- 配置项：`MaxMemoryRows` 为所有未结束事务内存行数之和的硬上限；
  `MaxSpillBlocks` 为溢写存储中块总数的硬上限。
- 每次 `Append` 后若内存行数之和超过上限，循环选出受害事务并把它**当前
  全部内存行**打包成一个溢写块、随即清空其内存，直到内存不超限。
- 受害事务选择：优先在「其他未结束事务」中选内存行数最多者，并列时取
  事务号最小；只有当其他事务都已无内存行、仍超限时，才溢写追加事务自身
  （自身溢写同样取最多内存/最小号，即其自身）。优先溢写他人可避免同一
  事务一次追加被自身拆块。
- 块号全局单调递增、**永不复用**；每个事务按生成顺序记录块内序号 `Seq`。

### 提交回放

- `Commit` 先按**块号升序**回放该事务的全部溢写块，再输出其当前内存行，
  输出顺序与该事务各行的追加顺序完全一致。
- sink 全部成功后，删除该事务全部块并把输出追加到已提交日志（提交序）；
  下游只能通过 sink 与 `LogSnapshot` 看到已提交事务。
- sink 或 `context` 失败时不发布、不删块，事务保持打开，提交可重试。

### 回滚

- `Rollback` 丢弃全部内存行并删除该事务全部溢写块，不产生任何输出，
  已提交日志不受影响。

### 拒绝类别（互不相同，可用 `errors.Is` 区分）

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidConfig` | 上限非正、sink 为 nil |
| `ErrInvalidTxn` | 事务号非正 |
| `ErrTxnExists` | `Begin` 事务号重复 |
| `ErrTxnNotFound` | 操作不存在或已结束的事务 |
| `ErrInvalidRows` | `Append` 空批次或空行 |
| `ErrStoreFull` | 本次追加需要的新块数超过溢写存储剩余容量 |
| `ErrInvariant` | `Check` 自检发现不变量被破坏 |

任一被拒操作都在改动状态前完成全部校验（含本次追加所需块数的预演），
**失败不留痕**：内存、块、事务计数与已提交日志均不变。

### 不变量与并发

- 任意时刻内存行数之和不超过 `MaxMemoryRows`；溢写块数不超过
  `MaxSpillBlocks`；每个溢写块都属于未结束事务（含 sink 输出中的提交中
  事务）。
- `LogSnapshot`、`QueryBlocks`、`QueryTxns`、`Check` 与计数接口均为读锁
  并发安全，可与 `Append`/`Commit`/`Rollback` 并发；提交时在锁外调用
  sink，慢下游不阻塞读取。
- `Config.Log`（`Logger.Printf`）逐步打印每步输入、内存行数与判定依据
  （如 `reason=max-memory,min-id` / `reason=self-over-limit,min-id`）。

### 本地验证

```bash
# 单元测试 + 竞态检测
go test -race -v ./spill

# 全量测试、覆盖率
go test -race ./...
go test -coverprofile=coverage.out ./...

# 带逐步日志的演示（溢写选择、提交回放、回滚、拒绝）
go run ./cmd/spill-demo

gofmt -l .
go vet ./...
```

测试覆盖：受害对象选择（最多内存/并列最小号/自我溢写）、提交回放顺序、
跨事务提交序、回滚清理、各类非法输入、溢写存储满拒绝及拒绝后状态不变、
块号不复用、sink 失败可重试、并发读写下的自检与上限不变量，并通过
`TestRandomEquivalenceWithNaiveReference` 与无上限朴素参照逐行比对。
