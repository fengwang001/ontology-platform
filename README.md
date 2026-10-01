# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## Multi-Paxos 槽位恢复规划器（`paxos` 包）

新主上任（Phase 1 结束）时，依据多数派接受者的承诺报告，为每个未决槽位
确定必须沿用的值，并为空洞补空操作（Noop），从而精确复现恢复计划与下一
个空闲槽位。

### 构造

`NewPlanner(b int64, n int)`：`b` 为本任新主选票（正整数），`n` 为接受者
数量（接受者编号 `0..n-1`）。`b <= 0` 或 `n <= 0` 返回
`ErrInvalidArgument`。

### 承诺报告字段（`PromiseReport`）

- `Ballot`（pb）：该承诺对应的选票，必须等于 `b`。
- `Chosen`：非负整数，表示该接受者已知槽位 `1..Chosen` 的值均已确定。
- `Accepted`：已接受表，键为槽位（正整数），值为
  `AcceptedEntry{Ballot 接受选票, Value 字符串}`；`Value` 可以是空串，
  空串是合法值，与 Noop 不同。

### `start` 与 `nextFree` 的推导

- `start = max(所有报告 Chosen) + 1`（空报告集合视为 max=0，即 start=1）。
- `maxSlot = 所有报告 Accepted 表中出现的最大槽位`；全部为空表时取 `0`。
- 仅槽位 `s ∈ [start, maxSlot]` 需要恢复；`maxSlot < start` 时无恢复条目。
- `nextFree = max(maxSlot, start-1) + 1`。因此当 `maxSlot < start` 时
  `nextFree == start`。

### 值选择与空洞补空规则

对每个待恢复槽位（按槽位升序处理）：

- 先在各报告中找出该槽位的最大接受选票 `best`。
- 再比较所有“接受选票恰为 `best`”的项：值必须完全一致，否则该槽位冲突，
  `Plan` 返回 `ConflictError{Slot}`，取最小的冲突槽位。
- 值不按接受者数量计数：单个高选票项压过多份相同的低选票项；低于 `best`
  的项彼此不同也不构成冲突。
- 若所有报告在该槽位都没有接受项，则补 Noop：`Noop=true`、`Value=""`、
  `SourceBallot=0`。注意 `Noop=false, Value=""`（某接受者确实接受过空串）
  与 `Noop=true` 可明确区分。
- 正常条目返回 `{Slot, Noop:false, Value, SourceBallot:best}`。

### 错误优先级

`AddPromise(from, report)` 按下列顺序只报第一个错误：

1. 规划器已关闭（`ErrClosed`）；
2. `from` 越界（`ErrAcceptorOutOfRange`）；
3. `pb != b`（`ErrWrongBallot`）；
4. 已接受表非法：各项按槽位升序检查，每项先查“槽位 `<=` 本报告自身
   Chosen（含槽位 0）”报 `ErrSlotNotAfterChosen`，再查“接受选票为 0 或
   `>= b`”报 `ErrInvalidBallot`；
5. 同一接受者重复提交（`ErrAlreadyPromised`）。

`Plan()` 按“已关闭（`ErrClosed`）→ 不足多数派（`ErrNoQuorum`，多数派为
`⌊n/2⌋+1` 个**不同**接受者）→ 冲突（`ConflictError`）”的顺序只报第一个。

被拒绝的 `AddPromise` 与失败的 `Plan` 不改变任何状态，且 `Plan` 失败不关闭
规划器；`Plan` 成功后规划器关闭，之后所有 `AddPromise`/`Plan` 均报
`ErrClosed`。

### 并发与确定性

所有操作经互斥锁串行化，结果等价于某个串行顺序；报告到达顺序不影响
`Plan` 结果；并发调用 `Plan` 至多一个成功，其余得到 `ErrClosed`。收集时对
报告做防御性拷贝，`Plan` 返回全新分配的切片，与内部状态及调用方输入均无
别名。

### 本地验证

```bash
# 全部用例（含竞态检测），-v 可看到每个用例打印的输入报告、Plan 输出与判定依据
go test -race -v ./paxos

# 随机对拍：400 组合法报告与逐槽位朴素计算 naivePlan 比较
go test -run TestRandomDifferentialAgainstNaive -v ./paxos

go vet ./...
gofmt -l .
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
