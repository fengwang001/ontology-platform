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

## 多视图全局一致读（`consistency` 包）

位于 `consistency/`，解决“各视图应用进度不一致、各自只保留有限历史版本”时的全局一致读。

### 基本概念

- **进度（progress）**：每个视图当前已处理到的时间戳，初始为 -1。`Apply(view, ts, value)` 追加一个版本并把进度推进到 `ts`；`Heartbeat(view, ts)` 只推进进度、不产生版本。两者都要求时间戳**严格前进**（`ts > 当前进度`）。
- **版本（version）**：每个视图一串时间戳严格递增的 `(ts, value)`。视图在时间点 `t` 的值取**时间戳不超过 `t` 的最大版本**（二分查找）。
- **有限保留（retention）**：每个视图只保留最近 `N` 个版本（`NewStore(N, ...)`）；更旧的版本在新版本写入时立即淘汰，**永久不可读**。版本不可变，淘汰只影响可读性，从不改写已有值。

### 读取规则（两道关卡，顺序固定）

对指定时间点 `t`（`ReadAt`）：

1. **进度关（尚未准备好 / `ErrNotReady`）**：每个视图都必须至少有一个版本（只有心跳不算），且 `t <= min(各视图进度)`。`min` 进度对应的就是“所有视图都已经应用到”的时间点上界。
2. **保留关（太旧 / `ErrTooOld`）**：对每个视图，其当前**最旧保留版本**的时间戳都必须 `<= t`；任一视图最旧版本晚于 `t`，该点已永久丢失。

两关都通过才返回快照，快照中的每个视图各自取不超过 `t` 的最大版本，因此快照对应同一个、所有视图都已应用的时间点。

`Read()` 返回最新的一致时间点（即当前的进度最小值），并用单调游标保证连续调用返回的时间点**单调不减**；若该点已被某视图淘汰（共享可读窗口 `[max(各视图最旧版本), min(各视图进度)]` 为空），返回 `ErrTooOld`。同一时间点只要还在保留窗口内，无论读多少次、期间其他视图如何推进，逐视图结果都**不变且可复现**。

### 边界约定

- 时间戳为非负整数；`t == minProgress` 与 `t == 某视图最旧版本` 都属于可读边界（闭区间）。
- 视图的第一个版本同时是该视图历史窗口的起点；早于任一首版本的时间点即使已过进度关，也按“太旧”拒绝。
- 视图靠心跳把进度推到更晚时，旧时间点读到的仍是旧版本（心跳不产生新版本）。
- 快照视图顺序固定为 `NewStore` 中声明的顺序，便于逐视图比对。

### 错误类别（互不相同，`errors.Is` 可区分）

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidArgument` | `retain<1`、无视图/重名/空视图名、未知视图、`nil` 值、负时间戳 |
| `ErrTimestampNotAdvancing` | `Apply`/`Heartbeat` 的时间戳不大于该视图当前进度 |
| `ErrNotReady` | 有视图尚无任何版本，或时间点超过各视图进度的最小值 |
| `ErrTooOld` | 存在视图其最旧保留版本晚于该时间点（版本已淘汰，永久不可读） |

参数校验先于一切状态变更，拒绝发生在任何写入之前；**任何一次被拒都不会改变进度、版本与保留情况（失败不留痕）**。

### 并发

- 内部使用 `sync.RWMutex`：多个 `Read`/`ReadAt` 可并发持有读锁，`Apply`/`Heartbeat` 与读取在同一把锁上安全并发。
- 单调游标用原子 CAS 推进，保证跨执行体的连续读取只进不退。

### 日志

`Store.SetLogger` 可注入日志函数（如 `log.Printf`、`testing.T.Logf`）。每一步都会打印操作输入、时间点、关键中间量（`minProgress`、`oldest`、游标、保留版本数）与判定依据（`reason=...`）。

### 本地验证

```bash
# 逐步演示：输入 / 时间点 / 判定依据（含各类拒绝）
go run ./cmd/demo

# 全量测试（随机对照朴素全历史参照 + 竞态检测，重复 10 次）
go test -race -count=10 ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

测试要点（`consistency/*_test.go`）：

- 与保留**全历史**的朴素参照模型做 3000 步 × 多组确定性随机序列对照，逐步比较错误类别与快照（`parity_test.go`）。
- 进度最小值与心跳落后、闭区间边界、首版本即窗口下界。
- 淘汰导致太旧、淘汰后同点结果不变、彻底淘汰后永久不可读。
- 四类非法/拒绝输入，以及拒绝前后逐字节状态不变（失败不留痕）。
- 连续读取单调不减、跨执行体全局单调、并发与写入竞争下结果仍符合全历史参照（`-race`）。
