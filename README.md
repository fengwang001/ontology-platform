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

## 三档触发翻滚窗口计数器（`window` 包）

按计数维护翻滚窗口，并以变更日志（`Change`，含全局单调 `Seq`）分档产出，
保证每个窗口的最终计数正确且同序重放可复现。

### 窗口与水位线

- 窗口按固定大小 `Size` **左闭右开**切分：`[start, start+Size)`，
  `start = ts - ((ts % Size) + Size) % Size`，支持负时间戳（如 `-1` 落入 `[-10, 0)`）。
- 水位线 `watermark = 已见最大事件时间 - Delay`，**只进不退**；
  批内每条事件（含被丢弃事件）都参与最大事件时间的更新。

### 三档触发的精确边界

设窗口为 `[start, end)`，`end = start + Size`，清除点为 `clearAt = end + LateLimit`：

| 档位 | 触发条件 | 产出 |
| --- | --- | --- |
| 早触发 `EARLY` | `watermark < end` 且计数达到 `EarlyEvery` 的整数倍 | 一条 `UPSERT` 中间快照，**计数不清零** |
| 准点 `ON_TIME` | `watermark >= end` | 一条 `UPSERT` 最终计数，**每窗口至多一次** |
| 迟到 `LATE` | 准点已触发且 `watermark < clearAt` | 先 `RETRACT` 旧值，再 `UPSERT` 新值 |
| 丢弃 | `watermark >= clearAt` | 不产出日志，丢弃计数 `Dropped() + 1` |

### 状态清除规则

- 当 `watermark >= end + LateLimit` 时，窗口状态被清除，此后落入该窗口的事件一律丢弃计数。
- 未清除窗口数超过 `MaxWindows` 时整批拒绝。

### 批次原子性与错误区分

- `NewCounter` 对非法参数返回 `*ConfigError`（按字段区分：`Size` / `EarlyEvery` / `Delay` / `LateLimit` / `MaxWindows`）。
- `AddBatch` 先预检再应用：空键返回 `*RejectError{Reason: EMPTY_KEY}`，
  未清除窗口数超限返回 `*RejectError{Reason: TOO_MANY_WINDOWS}`；
  **任一条被拒则整批不生效、状态不变**。
- `View` / `Dropped` / `SelfCheck` 均可并发调用；并发读取的视图逐字段相同。

### 本地验证：批量重算核对

`SelfCheck()` 用已接受事件日志**批量重算**各窗口计数，并与窗口状态及变更日志的
最新输出值逐窗口核对；`TestCrossCheckWithRecompute` 再用测试内独立的重算函数
对随机事件流（含负时间戳、乱序、迟到、丢弃）做交叉核对，并同序重放两次验证可复现：

```bash
# 全量测试（含竞态检测）
go test -race ./window/

# 查看事件、触发类型、变更日志与判定依据的详细日志
go test -race -v ./window/

# 只跑批量重算核对
go test -v -run TestCrossCheckWithRecompute ./window/
```
