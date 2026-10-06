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

## 预付费电表模块（prepaid）

`prepaid` 包实现预付费电表账户与远程停送电控制器：

- 账户余额随电表读数按区间起点电价扣减（金额向下取整），余额可为负。
- 余额跌破预警阈值记一次性预警；转负进入待停电，友好时段内推迟执行。
- 每结算周期可启用一次应急额度；充值按「还应急 → 按比例清偿欠费 → 入余额」分配。
- 已停电账户充值达复电阈值进入待复电，须在确认时限内确认才复电。
- 时钟跨越周期边界时记录周期汇总（扣费总额、预警次数、停电时长）。

设计取舍见 [prepaid/DESIGN.md](prepaid/DESIGN.md)。

```bash
go test ./prepaid -v        # 单元测试 + 朴素模型随机对照（逐条日志）
go test ./prepaid -race     # 并发串行等价性
go test ./prepaid -bench=.  # 性能基准
```
