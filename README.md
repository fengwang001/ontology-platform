# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 双流区间连接器（interval stream join）

`ontology` 包实现了一个并发安全的双流区间连接器，把左右两条**按事件时间非递减
到达**的事件流，按连接键（join key）和事件时间区间两两配对。

### 匹配条件（区间两端均闭合）

设左事件时间为 `L`、右事件时间为 `R`，配置的区间偏移为 `lower`、`upper`，
且要求 `lower <= upper`。一条左事件与一条右事件在**连接键相同**且满足下式时
配对，两端都取等号：

```
L + lower <= R <= L + upper
```

等价形式：`R - upper <= L <= R - lower`。
所有比较均直接比较 `L + 偏移` 与 `R`（内部使用溢出安全的 `sumCmp`），不做
`R - L` 差值运算，因此时间取到 `math.MinInt64` / `math.MaxInt64`、偏移取到
极值时也不会整数溢出。

### 水位线与单步处理顺序

每条流各自维护一个**单调前进**的水位线：左侧水位线 = 已接受的最大左事件时间，
右侧同理；两条流的水位线互相独立。一条事件到达时严格按以下顺序处理：

1. **校验**：连接键不能为空；事件时间不得小于本侧当前水位线（相等允许，即
   非递减）。
2. **推进本侧水位线**到该事件时间（仅在干跑全部通过后才真正提交）。
3. **匹配**：新事件与**对侧当前保留**且同键的事件逐条按闭合区间判定，收集
   本次新产生的配对（同键同刻的多条事件会一一配对）。
4. **清理两侧**：依据更新后的双水位线，删除已不可能再匹配的事件。
5. **保留容量检查**：清理完成后，两侧保留事件总数仍超过 `MaxRetained` 则
   整体拒绝。

匹配与清理都先在锁内**干跑**（dry-run），容量检查通过后才一次性提交水位线、
全局事件编号、保留状态与已输出配对；因此任何被拒绝的操作都**不会**留下半点
副作用。

### 清理规则（精确到 1ms）

未来事件时间不可能低于当前水位线：

- 左事件 `L`：未来右事件 `R' >= wmR`。当 `wmR > L + upper`（严格大于）时，
  连最小可能的 `R'=wmR` 都已越过闭合上界，删除。
- 右事件 `R`：未来左事件 `L' >= wmL`。当 `wmL + lower > R`（严格大于）时，
  连最小可能的 `L'=wmL` 都已越过闭合下界，删除。

注意是严格不等号：`wmR == L + upper` 或 `wmL + lower == R` 时事件**必须保留**，
因为区间两端闭合，恰好在边界上的未来事件仍然能成配（不漏对）。容量上限只约束
清理之后的状态，因此不会因为"暂时保留边界事件"而误拒合法输入；真正无法靠清理
压到上限内时才拒绝（不多留无法消化的状态）。

### 拒绝原因（可区分）

用 `errors.Is` 判定：

| 哨兵错误 | 触发条件 |
| --- | --- |
| `ontology.ErrInvalidArgument` | 构造配置非法：`LowerBound > UpperBound` 或 `MaxRetained <= 0` |
| `ontology.ErrEmptyKey` | 连接键为空字符串 |
| `ontology.ErrTimeRegressed` | 事件时间小于本侧当前水位线（单侧时间倒退） |
| `ontology.ErrRetentionExceeded` | 一步处理并清理后，两侧保留事件总数仍超过 `MaxRetained` |

### 并发与确定性

- 全部状态由一把 `sync.RWMutex` 保护：`ProcessLeft` / `ProcessRight` 互斥
  串行，`Snapshot` / `Pairs` 可与之及彼此并发（读锁）。
- 每个 `Snapshot` 是逐字段一致的只读副本：水位线、编号、两侧保留事件
  （按 `(key, time, seq)` 排序）、已输出配对（按提交顺序）。
- 配对只依赖输入，不依赖 goroutine 调度或时钟；同一输入序列重复执行得到
  完全相同的输出（无共享随机源、无 map 遍历顺序泄漏到结果）。

### 日志

默认以 `slog` 文本格式输出到 `os.Stderr`，逐条打印：接收的输入
（`input accepted`）、与每个候选事件的判定依据（`match check`，含
`lower_closed_ok` / `upper_closed_ok` / `matched` 字段）、输出的配对
（`pair emitted`）、清理事件及其依据（`event cleaned`）、被拒绝的操作及
原因（`input rejected`）。可用 `ontology.WithLogWriter(w)` 重定向或静默。

### 用法示例

```go
c, err := ontology.NewConnector(ontology.Config{
    LowerBound: -5,   // L-5 <= R
    UpperBound: 10,   // R <= L+10，区间 [-5,10] 两端闭合
    MaxRetained: 10_000,
})
if err != nil { /* errors.Is(err, ontology.ErrInvalidArgument) */ }

pairs, err := c.ProcessLeft("order-1", 100) // 左流：键、事件时间
pairs, err = c.ProcessRight("order-1", 108) // -> 命中 100-5<=108<=100+10

snap := c.Snapshot() // 并发只读：水位线、保留事件、全部已输出配对
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 本地验证

```bash
# 全量测试
go test ./...

# 带竞态检测（连接器是并发原语，建议始终带上）
go test -race -v ./ontology/

# 单个用例
go test -race -run TestStepByStepCleanupPrecision -v ./ontology/

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

测试覆盖：区间两端闭合的边界命中、逐步清理的精确性（边界保留 / 越界 1ms
即删）、四类非法输入及拒绝后零副作用、容量按两侧合计、溢出安全的极值时间、
并发读者逐字段一致性，以及日志中确实包含输入、判定依据、配对、清理与拒绝
原因。

```bash
# 代码检查
gofmt -l .
go vet ./...
```

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

### 单个包 / 单个用例

```bash
go test ./ontology
go test -run TestObjectType ./ontology
```
