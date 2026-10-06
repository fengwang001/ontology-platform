# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

### 拥堵收费结算服务（`congestion` 包）

多层嵌套收费区的拥堵收费与豁免结算，支持自然日首进计费、嵌套叠加与日封顶、
残障/新能源/居民三类资格的优先级、追溯登记退款、车牌变更与争议冻结。

```go
s := congestion.New(congestion.Config{
    Location: loc, DailyCap: 100, RetroDays: 3, DiscountBasis: 100,
})
_ = s.AddZone(congestion.Zone{ID: "outer", DailyFee: 60, /* Cells: 离散网格 */ ...})
_ = s.RegisterVehicle("car1", "A-001", t0)
_ = s.Enter("car1", "outer", t1)
rep, _ := s.Query("car1", "2026-01-02") // Payable / Lines / Adjustments
```

- 设计与取舍：`congestion/DESIGN.md`
- 逐条操作审计日志：`s.SetLogger(os.Stdout)`
- 独立朴素模型与随机差分：`congestion/naive_test.go`、`TestRandomDifferential`
- 查询复杂度验证：`BenchmarkQueryHistory100` 与 `BenchmarkQueryHistory10000`

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
