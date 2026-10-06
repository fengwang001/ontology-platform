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

## 协同搬家与装修服务

根包 `ontology` 提供可嵌入应用层的内存服务 `Service`：

- 货梯：`Reserve`、`Cancel`、`CheckInElevator`、`CheckOutElevator`、`SlotFree`。
- 装修：`ApplyPermit`、`ApprovePermit`、`ExtendPermit`、`CheckInPermit`、`CheckOutPermit`。
- 物业：`Complain`、`LiftSuspension`、`SetQuietRanges`、`AddHoliday`。
- 押金：`Deposit`、`Refund`、`Balance`；`Snapshot` 返回预约、许可、押金流水与判定日志。

关键规则：

- 错误固定按“非法参数 → 时钟回退 → 不存在 → 时间窗口 → 状态不允许 → 静音冲突 → 押金不足”只返回第一个错误。
- 所有时间由操作的 `now` 驱动；后台不推进时间，因此相同输入序列可确定性重放。
- 活动槽位使用 `map[电梯,分钟]`，同户同日使用 `map[户,日]`；历史记录不参与可预约或签到判定。
- `SlotFree` 与签到判定的查找次数不随历史预约数增长；`TestSlotLookupConstantInHistory` 构造 2000 条历史作可重复验证。
- 单个互斥锁保护跨预约、许可、押金的原子检查与提交；并发结果等价于某个串行顺序。
- 拒绝路径在状态推进前返回，测试验证被拒绝操作不留下预约、许可、押金或时钟变化。

随机对照测试位于 `naive_test.go`：朴素模型独立扫描活动预约与许可，不调用生产判定逻辑；每一步比较错误码，并比较预约状态、许可状态和押金余额。失败时打印完整操作日志、输入、输出和判定依据。

设计取舍见 `DESIGN.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
