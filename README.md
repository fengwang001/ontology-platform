# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 查看包文档
go doc ./meter
```

## 测试

```bash
# 全量测试（边界 + 400 条随机序列与独立朴素模型对照 + 资金不变量）
go test ./...

# 带竞态检测
go test -race ./...

# 单个用例
go test ./meter -run TestFriendlyBoundaries -v

# 性能：单操作不随历史增长，友好时段判定不随节假日数量增长
go test -run XXX -bench . ./meter

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 预付费电表（meter 包）

设计与取舍见 `meter/DESIGN.md`。核心能力：

- 区间按起点时刻电价、向下取整扣费；首条读数只登记。
- 余额跌破预警阈值一次性预警，回升到阈值之上才重置；阈值变更不触发。
- 扣费后为负排入停电，友好时段（每日窗口、周末、节假日）推迟；
  推进到执行时刻负余额转欠费。
- 每结算周期一次应急额度；周期边界重置资格但不清零未偿还额。
- 充值三段分配：先还应急 → 按比例（向下取整）清偿欠费 → 其余入余额。
- 已停电充值后达到复电阈值进入待复电，用户须在时限内确认；
  超时或待复电期间再跌破阈值则回到已停电。
- 跨周期停电时长按边界切分；相同操作序列重放得到完全相同的事件序列。
