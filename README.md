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

## 报警生命周期管理服务（`alarm` 包）

工业控制室报警的触发 / 返回 / 确认 / 手动屏蔽 / 震荡自动屏蔽 / 工况条件抑制 /
停用启用，活动报警列表（四层排序）与报警率查询。设计与取舍见
[`docs/DESIGN.md`](docs/DESIGN.md)。

### 快速体验

```bash
go run ./cmd/alarmsrv   # 端到端演示，打印每个操作的输入/输出/判定依据
```

### 对外 API（`alarm.New(cfg, points, logger)` 后使用）

- `Trigger(ts, id)` / `Return(ts, id)` / `Ack(ts, id, role, actor)`
- `Shelve(ts, id, role, durationSec, reason)` / `Unshelve(ts, id, role)`
- `Disable(ts, id, role, ticket)` / `Enable(ts, id, role, ticket)`
- `SetActiveConditions(ts, conds)`（系统工况信号，非操作员行为）
- `ActiveList(ts)` / `AlarmRate(ts, durationSec)` / `ShelvingOf(ts, id)`

错误按次序短路：参数非法 > 时钟回退 > 点不存在 > 无权限 > 状态不允许
（紧急不可屏蔽、已屏蔽、已停用等）> 屏蔽时长超上限，错误码见
`alarm.ErrInvalidArg` 等常量；被拒绝操作不改变任何状态与时钟。

### 复杂度对照（`go test ./alarm/ -bench .`，arm64 实测示例）

| 对照 | 档 A | 档 B | 结论 |
| --- | --- | --- | --- |
| 触发耗时 vs 历史事件总量 | 1 万历史，775 ns/op | 4 万历史，800 ns/op | 不随历史增长 |
| 列表耗时 vs 总点数（活动同为 500） | 总 1 万点，12.7 µs/op | 总 4 万点，9.3 µs/op | 与总点数无关 |
| 列表耗时 vs 活动条数（总点同为 4 万） | 500 条，9.3 µs/op | 4000 条，79.1 µs/op | 只随活动条数增长 |
| 报警率 vs 出现次数 | 1 万次，474 ns/op | 4 万次，490 ns/op | 二分查找，与历史规模无关 |

## 代码检查

```bash
gofmt -l .
go vet ./...
```
