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

## diffview：多重集差增量物化视图

`diffview` 包维护左右两侧行变更流的多重集差（left \ right）物化视图，并输出变更日志，下游按序应用日志后得到的视图与批量重算完全一致且可复现。

### 多重集差语义

- 每行结果重数 = `max(左侧重数 - 右侧重数, 0)`。
- 重数为零的行不出现在视图中。
- 每条输入变更只影响其所在行；结果重数变化时恰好输出一条日志（`Entry{Before, After}`），不变则不输出。
- 右侧先于左侧到达合法：此时结果仍为 0，不产生输出；左侧补齐后按差值输出。
- 任一侧任一行重数不得因删除变负（删除下溢会被拒绝）。

### 变更日志规则

- 日志条目按提交顺序编号（`Seq` 从 1 开始），记录触发输入（侧、行、增量）与结果重数变化（`Before`/`After`）。
- 任意前缀日志从空视图重放（`Replay(log, n)`）都等于对应前缀提交后的视图，且各行重数非负。
- `SelfCheck` 校验：全量日志重放 == 当前快照 == 由原始重数批量重算（`Batch`）的结果。
- `View` 内部使用 `sync.RWMutex`：`Apply` 串行提交，`Snapshot`/`SelfCheck`/`Log` 可与提交及其他执行体并发调用。

### 边界与错误类别

所有非法输入整体拒绝，失败不留痕（两侧重数、视图、日志均不变）。错误为互不相同的哨兵，可用 `errors.Is` 区分：

| 错误 | 触发条件 |
| --- | --- |
| `ErrEmptyRow` | 行键为空串 |
| `ErrZeroDelta` | 增量为零 |
| `ErrDeleteUnderflow` | 删除使任一侧该行重数变负 |
| `ErrRowLimit` | 插入新行导致两侧不同行键总数超过 `New(maxRows)` 上限（0 表示不限；已有行不受限） |

### 本地验证

```bash
# 单元测试（含右侧先到、删除下溢、最小变更、非法输入、拒绝后状态不变、随机序列对拍批量重算、并发自检）
go test ./diffview/

# 竞态检测 + 逐步日志（每步打印输入、输出与判定依据）
go test -race -v ./diffview/
```
