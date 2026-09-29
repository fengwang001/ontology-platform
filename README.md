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

## 成员状态合并与传播器（`membership` 包）

`membership` 包实现基于化身号（incarnation number）的成员视图合并与限次捎带传播，
使各节点在任意投递顺序与重复投递下收敛到相同视图，并支持被误判节点自证恢复。

消息为三元组 `(Member, Status, Incarnation)`，状态取值 `Alive` / `Suspect` / `Dead`。

### 覆盖（合并）规则

- **确认失效压过一切且不可逆**：`Dead` 覆盖任意 `Alive/Suspect`（无视化身号），一旦为 `Dead`，后续任何消息都被丢弃。
- **存活 `Alive(i)`**：仅当 `i > j` 时覆盖 `Alive(j)` 或 `Suspect(j)`（同化身号不能推翻可疑）。
- **可疑 `Suspect(i)`**：当 `i >= j` 时覆盖 `Alive(j)`；当 `i > j` 时覆盖 `Suspect(j)`。
- 未覆盖的更新直接丢弃，不进入待传播缓冲。
- 合并是基于偏序的交换、幂等运算，因此同一批消息以任意顺序、任意重复合并后，视图逐项相同。

### 自证与退出

- 节点收到关于**自己**且 `Incarnation >= 自身化身号` 的 `Suspect` 时，立即把化身号置为 `i+1`、
  视图置为 `Alive(i+1)`，并把该存活更新放入捎带队列传播出去（旧可疑因此被高化身号存活推翻）。
- 化身号更低的自指可疑按普通规则丢弃。
- 收到关于**自己**的 `Dead`：视图置为确认失效并退出（`Exited()==true`），此后
  `Receive` / `Tick` / `Generate` 一律返回 `ErrNodeExited`，且不改变任何状态。

### 超时升级

- 时钟只能由 `Tick(t)` 单调推进（注入时钟，回拨返回 `ErrClockRewind`）。
- 每条 `Suspect` 被接受时记录截止时刻 `deadline = 当前时钟 + SuspectTimeout`。
- 当时钟满足 `clock >= deadline`（**恰好等于即算超时**），该可疑自动升级为 `Dead` 并传播；
  被存活/高化身号可疑覆盖后截止时刻清除，不再升级。自己的可疑超时同样导致退出。

### 捎带传播规则

- 每条被接受的更新最多捎带 `λ · ⌈log2(n+1)⌉` 次（`n` 为名单人数），达到上限即移出队列。
- 每次 `Generate()` 返回至多 `B` 条外发消息；选择顺序为**已发次数升序，再按成员标识升序**。
- 同一成员的新更新**替换**队列中的旧更新，发送次数重新归零。
- 超时升级与自证存活也作为“被接受的更新”进入队列。

### 拒绝原因（可区分的哨兵错误）

| 错误 | 触发条件 |
| --- | --- |
| `ErrIncarnationNegative` | 化身号为负 |
| `ErrUnknownMember` | 成员不在名单中（含配置时 `SelfID` 不在名单） |
| `ErrInvalidParameter` | `Lambda <= 0` 或 `B <= 0` |
| `ErrNodeExited` | 对已退出节点执行接收/推进/生成 |
| `ErrInvalidStatus` | 消息状态值非法 |
| `ErrClockRewind` | `Tick` 目标时钟小于当前时钟 |

所有拒绝均为先校验、后生效：非法消息或含非法消息的整批接收不会改变任何视图或缓冲。

### 并发与确定性

- `Receive`、`ReceiveBatch`、`Tick`、`Generate`、`View` 均由同一互斥锁保护，可并发调用。
- 无随机源、无墙钟依赖；相同输入与相同时钟序列反复执行，视图、外发内容与日志完全一致。
- 日志（可通过 `Config.LogOutput` 注入 `io.Writer`）逐条打印 `input`、`output` 与 `basis` 判定依据。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细日志（含每条规则的判定依据）
go test -race -v ./membership/

# 仅看某个用例
go test -run TestSuspect5RefutedByAlive6 -v ./membership/

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

关键用例：

- `TestSuspect5RefutedByAlive6`：可疑 5 被存活 6 自证推翻并替换重计；
- `TestSameIncarnationSuspectDominatesAlive`：同化身号可疑压存活、存活不能翻案；
- `TestDeadTerminalAndIrreversible`：确认失效压过一切且不可逆；
- `TestSuspectTimeoutExactBoundary`：超时恰好到达即升级、未到不升级、被推翻后不升级；
- `TestCarryLimitBatchCapAndReplacement`：捎带上限、`B` 截断、替换重计；
- `TestConvergesUnderReorderAndDuplication`：乱序、重复、批量合并且视图逐项相同；
- `TestSelfRefuteAndExit`：自指可疑自证、自指确认失效退出；
- `TestRejectionsAreAtomicAndDistinguishable`：各类拒绝原因与原子性；
- `TestConcurrentReceiveTickGenerate`：并发接收/推进/生成（`-race`）；
- `TestDeterministicReplay`：相同输入与时钟序列重放完全一致。
