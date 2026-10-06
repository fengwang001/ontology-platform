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

## 押金争议处理服务（`deposit` 包）

`deposit/` 实现退房后扣项申报、固定次序受偿、租户争议冻结、裁定执行与法定退还时限违约责任。

- 设计与取舍：[`deposit/DESIGN.md`](deposit/DESIGN.md)
- 时限配置：`Config{A,B,C,RateNum,RateDen}`（申报期 A 天、争议期 B 天、退还期 C 天、日违约金率）。
- 主要 API：`CreateLease` / `Checkout` / `Declare` / `Revoke` / `Dispute` /
  `Adjudicate` / `Refund` / `Snapshot`。
- 错误码次序固定（参数非法→时钟回退→租约不存在→未退房→逾期→状态→金额越界），
  用 `deposit.CodeOf(err)` 或 `errors.Is(err, deposit.ErrXxx)` 判定。
- 五元桶守恒、争议/裁定 O(1)/条、随机序列与朴素模型对照、并发防重复退还，
  详见包内测试。

```bash
# 随机对照（-v 打印每步输入/输出/判定依据）
go test -run TestDifferentialRandom -v ./deposit

# O(1) 复杂度可验证基准
go test -run NONE -bench BenchmarkDisputeAdjudicate -benchtime=10000x ./deposit
```
