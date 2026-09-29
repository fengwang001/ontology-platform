# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 异步输出缓冲（`asyncio` 包）

`asyncio` 实现了一个异步输入输出算子的输出缓冲：它接收**元素**与**水位线**
交织的输入流，元素的异步处理可能乱序完成，由缓冲决定最终的输出顺序。
所有方法均为并发安全，可被多个执行体同时调用。

### 基本概念

- 元素：通过 `PushElement(id, value)` 输入。`id` 为调用方声明的唯一标识；
  元素异步处理结束后由调用方调用 `Complete(id)` 声明完成。
- 水位线：通过 `PushWatermark(wm)` 输入，必须相对上一条**严格递增**。
  水位线是一道屏障，表示其之前的全部元素都应在它之前输出。
- 容量与占用：`Stats().InFlight` 是“已接收但尚未 `Complete`”的元素数。
  只有在途元素占用容量；元素一旦声明完成即释放占用（即使它仍在等待输出）。
  `InFlight == Capacity` 时新元素被拒绝。
- 输出事件为 `Event`：`KindElement` 携带 `ID/Value`，`KindWatermark` 携带
  `Watermark`；`Seq` 是元素与水位线统一的输入序号，用于核对顺序。

### 两种输出模式

- `Ordered`（有序）：输出顺序严格等于输入顺序。缓冲只看队头——队头是已完成
  元素即输出；队头是水位线时，只要其之前的元素全部输出即输出水位线；队头被
  未完成元素阻塞时，其后任何已完成元素与水位线都等待。完成声明可能乱序，
  但输出保序。
- `Unordered`（无序）：相邻水位线把流划分为段落（段 0 为第一条水位线之前）。
  输入一条水位线后，新元素归入下一段；段 `k` 在其前面的 `k` 条水位线全部
  输出后才“开放”。
  - 已开放段内的元素一完成就按**完成声明先后**立即输出；
  - 未开放段内完成的元素被扣住；
  - 某条水位线之前的元素全部输出后立即输出该水位线，并级联开放后续段；
    新开放段中被扣住的元素按完成先后依次释放，之后继续判定下一条水位线。
  - 水位线之间的相对顺序与“先元素后其屏障”的约束始终保持不变。

### 输出消费

- `PopOutputs()`：一次性取出并清空当前已就绪输出（同步轮询/测试用）。
- `SnapshotOutputs()`：只读副本，不移除。
- `Drain(ctx, ch)`：阻塞式把输出泵入调用方的 channel，无输出时等待新事件或
  `ctx` 取消；可由多个执行体并发调用，每条事件恰好被取走一次。请用取消
  `ctx` 终止 Drain，不要在 Drain 运行期间关闭 `ch`。
- `Stats()`：返回 `InFlight / Capacity / Buffered / Pending` 快照。

### 错误类别（哨兵错误，`errors.Is` 区分）

任何一次被拒都在状态变更前返回，队列、占用数、已产生输出均不变（失败不留痕）。

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidCapacity` | 构造时容量非正 |
| `ErrInvalidMode` | 构造时模式取值非法 |
| `ErrEmptyID` | 输入元素或完成声明使用空标识 |
| `ErrDuplicateID` | 输入标识与仍在队列中的元素重复（元素输出后标识可复用） |
| `ErrCapacityFull` | `InFlight` 已达容量上限 |
| `ErrUnknownID` | 完成声明引用了队列中不存在的标识 |
| `ErrAlreadyCompleted` | 对已声明完成、尚未输出的元素重复完成 |
| `ErrWatermarkNotIncreasing` | 水位线未相对上一条严格递增 |

输入元素时的校验顺序为：空标识 → 重复标识 → 容量已满；所有错误互不等价。

### 边界与说明

- 容量为正整数；构造参数 `logger` 传 `nil` 时使用 `slog.Default()`，
  每一步输入、完成、输出与拒绝原因都会记录，输出日志含 `reason/seq/seg/
  in_flight/done_at/mode` 等判定依据字段。
- 标识在元素从队列中移除（已输出）后允许再次使用。
- `Complete` 先释放占用再推进输出，因此“完成但等待输出”的元素不再挡容量。
- 多执行体并发 `Drain` 经共享 channel 汇合时，跨执行体的接收先后不代表输出
  判定先后；要求严格接收顺序时使用单个 Drain 或 `PopOutputs`。

### 本地验证

```bash
# 全量测试（等价性测试会用朴素模型对数百条随机交织序列逐条比对）
go test ./...

# 竞态检测（并发输入/完成/Drain 场景）
go test -race -count=2 ./...

# 详细日志：每步输入、完成、输出与拒绝原因
go test -v ./asyncio

# 仅看有序保序 / 无序跨段级联场景
go test -v -run 'TestOrderedPreservesInputOrder|TestUnorderedHeldAndCascade' ./asyncio

gofmt -l .
go vet ./...
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
