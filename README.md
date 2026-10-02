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

## windowbuf：带宽限期与容量策略的窗口结果抑制缓冲

`windowbuf` 包把按窗口更新的结果压住不发，直到流时间越过窗口结束加宽限期后，
才作为最终结果发出；缓冲满时按策略早发或拒绝。

### 定义

- **窗口**：宽度为 `S`，时间 `ts` 属于窗口 `[ws, ws+S)`，其中 `ws = floor(ts/S)*S`，
  窗口结束 `end = ws+S`。
- **关闭时刻**：`end + G`（`G` 为宽限期）。缓冲条目只有在流时间不小于其关闭时刻后
  才作为 `Final` 发出。
- **流时间 ST**：初值 `-1`，单调不减。`Update` 把它推进到 `max(ST, ts)`，
  `Tick(t)` 把它推进到 `t`（`t < ST` 被拒绝，`t == ST` 为空操作）。
- **缓冲**：键为 `(key, ws)`，值为 `(value, lastTs)`；容量上限 `E`。

### Update 的处理顺序

1. **参数非法**：key 为空或 `ts`/`value` 越界，整体拒绝，状态不变。
2. **迟到**：若 `end+G <= ST`（用更新前的 ST），本条被丢弃，迟到计数加一，
   其余状态不变。
3. **推进**：`ST' = max(ST, ts)`；关闭集合为缓冲中关闭时刻不大于 `ST'` 的全部
   既有条目。本次更新自身推进 ST 而关闭的条目会先于容量判定腾出名额。
4. **容量**：`rest = 缓冲条目数 - 关闭集合大小`，`needNew` 为 1（新条目）或 0
   （覆盖）。若 `rest+needNew > E`：`SHUTDOWN` 拒绝（ST、缓冲、迟到计数全不变）；
   `EMIT_EARLY` 则从不在关闭集合内的既有条目中按 `(end, key)` 升序取前
   `x = rest+needNew-E` 个早发，本次写入的条目永不被早发。
5. **生效**：先按 `(end, key)` 升序发出关闭集合（`Final`），再按同序发出早发条目
   （`Early`），发出项含 `(key, ws, value, lastTs, 标记)` 并移出缓冲；随后写入或
   覆盖 `(key, ws) = (value, ts)`（覆盖时 `lastTs` 取本次 `ts`，即使更小），
   `ST = ST'`。

### 满时策略的取舍

- **EMIT_EARLY**：宁可提前发出未到期结果也不停机。吞吐不中断，但下游会收到
  `Early` 结果；同一 `(key, ws)` 被早发后若再被更新，会作为新条目重新入缓冲，
  并在其关闭时刻后以 `Final` 再发一次——下游需按 `(key, ws)` 覆盖式消费。
- **SHUTDOWN**：拒绝使缓冲超限的更新，保证只发 `Final` 结果，语义最干净，
  但写入方必须处理拒绝（重试或扩容），否则结果丢失。

### 并发与可复现性

所有操作与查询都可并发调用，内部以互斥锁串行化，`Update` 与 `Tick` 各是一个
原子步骤，观察者看不到只发出一部分条目的中间状态。任意时刻缓冲条目数不超过
`E`；ST 单调不减；`Final` 项的关闭时刻均不大于发出后的 ST；同一批次内 `Final`
先于 `Early`；每个缓冲条目至多被发出一次。相同操作序列重放得到完全相同的发出
序列、缓冲与计数。

### 本地验证

```bash
go test ./windowbuf/            # 全部单元测试（含 2000 组随机序列对照朴素模拟）
go test -race -v ./windowbuf/   # 竞态检测 + 打印每步输入、输出与判定依据
go test -run TestPeeksTwoTiers -v ./windowbuf/  # peeks 计数器两档对照
```
