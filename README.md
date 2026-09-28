# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## window：带水位线与迟到容限的翻滚窗口计数器

`window` 包把带事件时间的输入按固定大小的翻滚窗口分组计数，窗口到期时产出结果。

### 窗口划分

- 窗口大小 `Size` 固定，按数学地板划分、左闭右开：
  `...、[-2S,-S)、[-S,0)、[0,S)、[S,2S)、...`，事件时间可正可负。
- 事件时间 `ts` 属于窗口 `[ts - ts mod S, ts - ts mod S + S)`（`mod` 为数学取模，
  故 `ts=-1, S=10` 归入 `[-10,0)`，`ts=-10` 恰好落在边界时归入 `[-10,0)`）。

### 水位线

- 水位线 `watermark = 见过的最大事件时间 - Delay`，只随最大事件时间单调前进，
  乱序的旧事件不会使其回退。

### 触发、迟到与清除

- **触发**：`watermark >= 窗口结束时间` 时窗口触发，输出该键该窗口的计数（边界为闭区间）。
- **迟到容限**：触发后的窗口在 `watermark <= 结束时间 + AllowedLateness` 期间仍接受
  迟到事件，每接受一条就输出一次修正值（`corrected`）。
- **清除**：`watermark >= 结束时间 + AllowedLateness` 时窗口被清除并释放槽位；
  之后落入该窗口的事件被丢弃并计入 `Dropped`（边界相等即丢弃）。
  `AllowedLateness = 0` 时窗口在触发的同一轮即被清除。
- 一个此前为空的窗口若首个事件晚于触发线到达但仍在容限内，则接受并立即触发输出。

### 拒绝规则（全部整体拒绝、状态不变）

| 场景 | 哨兵错误 |
| --- | --- |
| 窗口大小非正 | `ErrNonPositiveSize` |
| `Delay` 为负 | `ErrNegativeDelay` |
| `AllowedLateness` 为负 | `ErrNegativeAllowedLateness` |
| `MaxOpenWindows` 为负 | `ErrInvalidMaxOpenWindows` |
| 事件键为空 | `ErrEmptyKey` |
| 同时保留的未结算窗口数超限 | `ErrTooManyOpenWindows` |

`Process` 以批为单位原子执行：先在状态的深拷贝上运行整批，全部成功才一次性提交，
因此任何被拒绝的批次都不会改变水位线、丢弃数或已产生的输出，之后仍可继续正常使用。
`MaxOpenWindows = 0` 表示不限制未结算窗口数。

### 并发与可复现

- `Snapshot()` 在读锁下返回深拷贝（结果按 `(Key, Start)` 排序、输出按 `Seq` 排序），
  多个并发只读得到的结果逐字段一致，且不受后续处理影响。
- 同一输入序列反复计算得到完全相同的输出（触发/清除按 `(键, 起点)` 确定顺序遍历）。

### 日志

`Config.Logger` 非空时，每个事件的处理都会打印输入、水位线推进、缓冲/触发/修正/
丢弃/清除及判定依据（如 `reason="watermark 10 >= gc-time 10"`）。日志也是事务性的：
成功提交的批次才刷出明细，被整体拒绝的批次只输出一行 `reject` 原因，
不会留下实际未生效的幻影操作记录。

### 本地验证

```bash
# 单元测试 + 竞态检测（覆盖负时间戳、边界迟到、触发与清除、非法输入、并发读取）
go test -race -v ./window/

# 可执行示例（输出被 go test 校验）
go test -run ExampleCounter -v ./window/
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
