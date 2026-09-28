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

## 流与版本表时态连接（`temporal` 包）

`temporal.Joiner` 把事件流中的每条事件连接上**其事件时间那一刻生效的版本**，
在版本变更晚到与水位线推进并发交错的场景下仍保证结果正确。

### 版本区间规则

- 每个键（`Version.Key`）的版本按 `EffectiveAt`（生效起点）升序排列。
- 区间**左闭右开**：版本 `v@t` 在 `[t, next_t)` 上生效；最后一个版本的区间是
  `[t, +∞)`。
- `Tombstone=true` 是墓碑，表示该键在该区间内**无值**（删除语义）。
- **同一生效起点**的变更覆盖既有记录（值覆盖值、墓碑覆盖值、值覆盖墓碑皆可）。
  起点一旦被水位线“封存”（见迟到判定），同点覆盖即按迟到拒绝，因此事件观察到的
  永远是封存前最后一次覆盖的结果。

### 查询（连接）规则

对事件 `(key, eventTime)` 做朴素时态查询：

1. 取该键下满足 `effectiveAt <= eventTime` 且**生效起点最大**的版本；
2. 不存在这样的版本，或命中的是墓碑 ⇒ 输出 **MISS（未命中）**；
3. 否则输出 **HIT**，携带命中版本的 `EffectiveAt` 与 `Value`。

事件时间**恰好等于**生效起点时命中该版本（左闭的直接推论）。

### 水位线、缓冲与迟到判定

- 内部维护单调水位线 `w`（初始为 `-∞`，`Drain()` 后为 `+∞`）。
- `eventTime <= w` 的事件：所有 `effectiveAt <= eventTime` 的版本必已到齐
  （见迟到规则），事件**立即确定**。
- `eventTime > w` 的事件：进入缓冲；`AdvanceWatermark` 推进水位线后，所有
  `eventTime <= 新水位线` 的缓冲事件按接受顺序确定，每个事件**恰好输出一次**。
- 版本变更的接受条件是 `effectiveAt > w`（严格大于）：
  - `effectiveAt > w`：接受（允许“晚于事件到达但早于水位线”的合理晚到）；
  - `effectiveAt <= w`：拒绝为迟到（`ErrLateVersion`）——水位线已向下游承诺
    更早的时间点之前不会再有版本，迟到写入若生效会改写已确定的结果。
- `AdvanceWatermark` 只能单调推进；回退返回 `ErrWatermarkRegression`。
- 缓冲事件数达到 `bufferLimit`（`NewJoiner` 配置，`<=0` 为不限）时，新的需缓冲
  事件返回 `ErrBufferLimitExceeded`；立即确定的事件不占缓冲。
- 空键（事件或版本）返回 `ErrEmptyKey`。
- **任何被拒绝的操作都不会改变版本表、水位线、缓冲与已输出结果**（错误先于一切
  状态写入判定）。

### 并发与确定性

所有方法均可被多 goroutine 并发调用（单锁保护全部状态）：

- 每个被接受事件恰好输出一次，结果与上述朴素查询一致；
- 同一**串行**输入序列反复计算，输出逐字节一致（见
  `TestExactlyOnceAndDeterministicReplay`）；
- 并发提交下，输出内容（事件 → 命中版本/未命中）同样确定，仅接受顺序可能随调度
  变化；`Results()` 始终按事件接受顺序（`Seq`）返回。

### 日志

`NewJoiner` 默认写入标准 logger，`NewJoinerWithLogger(lim, logger)` 可自定义，
传 `nil` 关闭。日志逐条打印：

- **输入**：`version{...}` / `event{...}`、接受或拒绝、缓冲；
- **连接结果**：`HIT effectiveAt=...` / `MISS ...`；
- **判定依据**：如 `largest effectiveAt <= eventTime=...`、
  `no version with effectiveAt <= ...`、`tombstone at effectiveAt=...`；
- 迟到（`late (effectiveAt <= watermark)`）、回退（`regression`）、
  空键、缓冲超限等拒绝原因。

### 本地验证

```bash
# 单元测试（含竞态检测），覆盖：
#   起点恰好等于事件时间、墓碑查询、同点覆盖、迟到/回退/空键/超限等非法输入、
#   拒绝无副作用、确定性重放、并发恰好一次、日志内容
go test -race -v ./temporal

# 覆盖率（当前 98%+）
go test -cover ./temporal

# 运行可执行示例并观察输入 / 连接结果 / 判定依据日志
go test -run ExampleJoiner -v ./temporal
```

最小用法：

```go
j := temporal.NewJoiner(1000) // 缓冲上限 1000
j.ApplyVersion(temporal.Version{Key: "k", EffectiveAt: 10, Value: "v10"})

st, r, err := j.ProcessEvent(temporal.Event{Key: "k", EventTime: 12})
// st == StatusBuffered（当前水位线为 -∞）

out, _ := j.AdvanceWatermark(20)
// out[0] == HIT effectiveAt=10 value=v10
```

