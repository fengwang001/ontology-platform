# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## CAN 故障界定状态机（`canfsm` 包）

`canfsm` 实现 CAN 控制器的故障界定（fault confinement）状态机：按发送/接收事件更新发送错误计数 TEC 与接收错误计数 REC，由计数导出节点状态，并建模总线关闭后的恢复流程。所有操作与查询均可并发调用，结果等价于某个串行顺序（内部以互斥锁串行化）。

### 事件对计数的影响

`Apply(ev)` 按**事件发生前**的状态处理：

| 事件 | 效果 |
| --- | --- |
| `TxOK` | TEC > 0 时 TEC -= 1（TEC 为 0 不下溢） |
| `TxErr` | TEC += 8 |
| `TxAckErr` | 事件前为主动错误态：TEC += 8；事件前为被动错误态：TEC、REC 均不变 |
| `RxOK` | REC > 127 时 REC = 127；否则 REC > 0 时 REC -= 1 |
| `RxErr` | REC += 1 |
| `RxErrDominant` | REC += 8 |

计数不设上限。初始 TEC = REC = 0。

### 三态导出规则

状态完全由计数导出：

- **总线关闭（BusOff）**：TEC > 255；
- **被动错误（ErrorPassive）**：TEC > 127 或 REC > 127（且 TEC ≤ 255）；
- **主动错误（ErrorActive）**：其余情况（TEC ≤ 127 且 REC ≤ 127）。

因此非总线关闭时 TEC ≤ 255；总线关闭时 TEC > 255 直至恢复结束。

### 总线关闭与恢复

- 总线关闭时 `Apply` 一律被拒绝（`ErrBusOff`）。
- `Restart()` 仅在总线关闭且尚未开始恢复时有效：开始恢复并把恢复计数置 0；否则按顺序返回 `ErrNotBusOff`（不在总线关闭）或 `ErrAlreadyRecovering`（已在恢复中）。
- `Idle11()` 仅在恢复中有效（否则 `ErrNotRecovering`）：每次恢复计数 += 1，计到 128 时 TEC 与 REC 清零、状态回到主动错误、恢复结束。
- `Apply` 先判事件非法（`ErrInvalidEvent`）再判总线关闭。
- 每次状态改变追加一条迁移记录 `{Op, From, To}`：`Op` 为第几个成功操作（成功操作含 `Apply`、`Restart`、`Idle11`，序号从 1 起）；相邻记录的新旧状态首尾衔接。
- 被拒绝的操作不改变计数、恢复计数、迁移记录与成功操作序号。

### 本地验证

```bash
# 全部测试（含竞态检测与详细日志：打印输入、输出与判定依据）
go test -race -v ./canfsm/

# 仅静态检查
go vet ./canfsm/ && gofmt -l canfsm/
```

测试覆盖：TEC 127/128 的状态差异、TEC 247→255 仍为被动错误而 248→256 进入总线关闭、REC 130 时 `RxOK` 置 127 回到主动错误、REC 127 时 `RxErr` 进入被动错误、`TxAckErr` 主动态 +8 被动态不变、`TxOK` 不下溢、第 127/128 次 `Idle11` 的恢复边界、恢复中再次 `Restart` 被拒、被拒操作不占成功序号，以及与逐步朴素模拟的随机重放对照和并发线性一致性检查。

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
