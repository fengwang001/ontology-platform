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

位于仓库根的三个包：

- `matching/`：单品种限价撮合引擎（生产实现）。入口 `matching.New()`，
  方法 `Submit / Cancel / ReplaceQty / BestBid / BestAsk / VisibleDepthAt /
  SnapshotDepth / GetOrder / Fills`。完整规则、取舍与复杂度论证见
  `matching/DESIGN.md`。
- `naive/`：按规格文字直译的独立朴素模型（切片+线性扫描），仅供对照。
- `difftest/`：双实现随机差分、守恒不变量、重放确定性与并发竞态测试；
  失败时打印输入/输出/判定依据日志。

```go
e := matching.New()
e.Submit(matching.OrderParams{ClientID: 1, Side: matching.Sell,
    Price: 100, TotalQty: 10, Type: matching.Iceberg, IcebergVisibleQty: 3})
seq, fills, err := e.Submit(matching.OrderParams{ClientID: 2, Side: matching.Buy,
    Price: 100, TotalQty: 20, Type: matching.Limit})
```

错误用 `*matching.EngineError` 的 `Kind` 区分，优先级为
参数非法 > 编号重复 / 委托不存在 > 已完成（改量、撤单），被拒不占序号、不留状态。

专项验证：

```bash
go test -race ./...
go test -v ./difftest/ -run TestDifferentialLogging
go test -run=NONE -bench=. ./matching/
```
