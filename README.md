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

## 翻滚窗口三档触发计数（`window` 包）

按计数维护翻滚窗口，并以变更日志（changelog）分档产出，保证每个窗口的最终计数正确且可复现。

### 窗口与水位线

- 窗口按固定大小 `WindowSize` **左闭右开**切分：`[floor(ts/size)*size, +size)`，负时间戳按数学向下取整（如 `-1∈[-10,0)`、`-11∈[-20,-10)`）。
- 水位线 = 已见最大事件时间 − `WatermarkDelay`，**只进不退**；任何事件（含被丢弃的迟到事件）都只可能推进、绝不回退水位线。
- 事件的迟到判定以其**到达前**的水位线为准。

### 三档触发的精确边界

| 档位 | 触发条件 | 产出 |
| --- | --- | --- |
| 早触发 `EARLY` | 水位线 < 窗口右边界，且窗口计数每达到 `EarlyEvery` 的整数倍 | 中间快照，**计数不清零** |
| 准点 `ON_TIME` | 水位线 ≥ 窗口右边界（达到即触发） | 最终计数，**每窗口至多一次** |
| 迟到 `LATE_RETRACT` + `LATE_UPDATE` | 准点后事件到达，且水位线 < 右边界 + `AllowedLateness` | 先撤回旧值再发新值（计数 +1） |

### 迟到与清除规则

- 水位线 ≥ 右边界 + `AllowedLateness` 时，迟到事件**丢弃**并计入 `Dropped()`。
- 水位线 ≥ 右边界 + `AllowedLateness` 时，窗口状态**清除**；`AllowedLateness = 0` 表示准点即清除、迟到一律丢弃。
- 准点后、清除前到达的迟到事件若窗口从未有过事件，会补建状态（标记准点已过），按撤回 0 / 更新 1 产出。

### 批量原子性与拒绝原因

`Ingest` 以批为单位原子生效：批内任一条被拒则整批不生效、状态不变。拒绝原因可通过 `*RejectError.Reason` 区分：

- `INVALID_CONFIG`：非法配置（`WindowSize`/`EarlyEvery`/`MaxOpenWindows` ≤ 0，`WatermarkDelay`/`AllowedLateness` < 0）。
- `EMPTY_KEY`：批内存在空键事件。
- `TOO_MANY_WINDOWS`：未清除窗口数将超过 `MaxOpenWindows`。

### 并发

`Snapshot()`、`Dropped()`、`FinalCounts()`、`SelfCheck()` 均可并发调用；`Snapshot` 返回深拷贝视图，同一时刻并发读取逐字段相同。`SelfCheck` 校验水位线单调性、清除时机、准点唯一性、早触发倍数与迟到撤回/更新配对等不变量。

### 本地验证：批量重算核对

用独立的最小模型（`RecomputeFinalCounts`，只复现接受/丢弃语义）重算每个窗口的最终计数，与引擎输出（`FinalCounts`：未清除窗口取实时计数、已清除窗口取变更日志最终值）逐窗口比对：

```bash
# 单测内置 20 组随机种子（含负时间戳与迟到）的交叉核对
go test ./window -run TestBatchRecomputeCrossCheck -v

# 或用命令行工具核对自定义事件文件（格式见 cmd/wincheck/main.go 注释）
go run ./cmd/wincheck events.json
```
