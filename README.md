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

## 内容寻址去重块库与两阶段回收器

实现在 `blockstore` 包（`blockstore/blockstore.go`）。块按内容摘要（SHA-256）
寻址，天然去重；备份写入以“会话”为单位，先 `Upload` 缺失块（已存在则复用），
最后 `Commit` 原子发布一份快照清单（manifest）。回收由 `Mark`（第一轮）与
`Sweep`（第二轮）两个阶段组成，可与上传、提交、读取并发调用。

### 块状态机

每个块在任意时刻恰处于三种状态之一：

- `normal`：正常块，可读。
- `pending`：第一轮标记的“当前无清单引用”块；**不删除、仍可读取**。
- `deleted`：第二轮物理删除后的状态（记录即不存在，读取返回 `ErrBlockDeleted`）。

### 两轮回收与安全条件推导

记某轮 GC 的 `Mark` 时刻为 T。

1. `Mark` 在互斥临界区内线性化地完成两件事：
   - 计算 T 时刻所有已提交清单引用的并集 R(T)，把不在 R(T) 中的 `normal` 块置为
     `pending`；
   - 快照 T 时刻所有仍在进行（open）的会话集合 S(T)。
2. `Sweep` 仅当 S(T) 中每个会话都已结束（提交或显式 `End`）时才执行删除；
   只要还有一个登记会话未结束，本轮**什么都不删**，周期保持可重试。

安全性论证（为何已提交快照永不丢块）：

- T 时刻已提交清单引用的块属于 R(T)，在 `Mark` 中不会被标 `pending`，因此不会被
  本轮删除。
- T 时刻未提交、但在 T 之前已经开始的会话都属于 S(T)。这样的会话能引用的块只有
  两种来源：T 时已存在的块（若 T 时无引用则已被 `Mark` 置 `pending`，故仍可读，
  不会在它结束前被删），或它在 T 之后自行上传的新块（新块为 `normal`，本轮不
  触碰）。等该会话结束后 `Sweep` 才删除，因此它引用的块必然存活到提交完成。
- T 之后才开始的会话不可能依赖任何 T 时刻的 `pending` 块而不先“触碰”它：上传
  相同内容会复用并恢复，提交清单要求每个引用块都存在，否则整体拒绝。
- `Sweep` 删除前重新计算当前引用并集：期间被新清单引用的 `pending` 块恢复为
  `normal`，只有“此刻仍无任何清单引用”的 `pending` 块才真正删除。

因此在任意交错下，每个已提交清单引用的每个块在读取时都存在。

### 待删（pending）块恢复规则

- 会话 `Upload` 命中一个 `pending` 块（内容相同，去重复用）：立即恢复为
  `normal`，并从当前 GC 周期的待删集合移除。
- `Commit` 成功发布清单时，清单中所有 `pending` 块恢复为 `normal`。
- `Sweep` 删除前重新核对引用，被新清单引用的 `pending` 块同样恢复为 `normal`。

### 提交的原子拒绝（互不相同的原因）

`Commit` 在做出任何状态变更前先完成全部校验，失败时不留清单、不改变任何块状态：

- 清单引用了从未上传且库中不存在的块：`ErrBlockMissing`。
- 会话不存在或已结束（`End` 过、或从未 `BeginSession`）：`ErrSessionNotFound`。
- 同一会话重复提交：`ErrDuplicateCommit`。
- 上传新块会使已用容量超过 `Capacity`：`ErrCapacityFull`（已存在块复用不计容量）。

### 并发、确定性与活性

- 所有操作（上传、提交、读取、`Mark`、`Sweep`）在同一把互斥锁内线性化，天然串行
  等价；`go test -race` 下验证。
- 块状态只能是 `normal`/`pending`/`deleted`；删除集合仅由操作序列及其线性化顺序
  决定，同一序列与同一交错顺序得到相同的删除集合（`TestDeterministicDeletionSet`）。
- 活性：一个始终无人引用的块，在一次“`Mark` 后没有长会话跨越、且至 `Sweep` 时
  仍无新清单引用”的完整周期后必然被删；`Sweep` 被长会话阻塞时不消耗周期，可反复
  重试，会话结束后同样删除。唯一的“延迟删除”来源是真实的在途写入，符合安全要求。

### 日志

`Config.Log` 可传入 `io.Writer`，每次操作以结构化文本记录输入（会话、摘要、容量
等）、输出（结果、删除/恢复集合）与判定依据（`reason`/`decision`），例如容量拒绝、
`Sweep` 因登记会话未结束而放弃删除。

### 本地验证

```bash
# 全量测试（竞态检测 + 详细日志）
go test -race -v ./blockstore/

# 交错压力：重复运行并发安全与确定性用例
go test -race -count=20 -run 'TestConcurrentInterleavingSafety|TestDeterministicDeletionSet' ./...

# 静态检查与格式
go vet ./... && gofmt -l .

# 覆盖率
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out
```

关键用例：`TestMarkThenLateCommitRevivesPending`（第一轮前已开始、第二轮前才提交
并引用待删块）、`TestReuseOfPendingRestoresNormal`（复用待删块）、
`TestLongSessionBlocksSweep`（长会话阻止第二轮）、
`TestUnreferencedBlockDeletedAfterTwoPhases`（无人引用经两轮删除）、
`TestCapacityFull`（容量已满）、`TestCommitMissingBlockIsAtomic` 与
`TestCommitSessionErrors`（四类可区分拒绝）。
