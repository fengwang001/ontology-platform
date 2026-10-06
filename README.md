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

## 限价撮合引擎（冰山 / 隐藏委托）

单品种限价撮合引擎位于 `matcher/`，支持普通、冰山（分批显示、补批失位）与隐藏委托；
独立朴素对照模型位于 `matcher/naive/`，设计取舍见 `matcher/DESIGN.md`。

```bash
# 引擎单元 + 随机对照（与朴素模型逐条比对）
go test ./matcher/...

# 带输入/输出/判定依据的逐条日志
go test ./matcher/ -run TestDifferentialRandom -v -difflog

# 竞态检测
go test ./matcher/... -race
```

最小用法：

```go
e := matcher.NewEngine()
e.Submit(matcher.NewOrderRequest{ID: "s1", Side: matcher.Sell, Price: 100,
    Quantity: 10, Kind: matcher.Iceberg, DisplaySize: 2})
res, _ := e.Submit(matcher.NewOrderRequest{ID: "b1", Side: matcher.Buy,
    Price: 100, Quantity: 3, Kind: matcher.Plain})
// res.Trades 为本次成交序列；e.GetOrder / BestBid / BestAsk / Trades 为只读查询。
```
