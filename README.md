# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 支付商户延迟结算与滚动保证金系统

本仓库当前交付内容为 `settlement/` 包：按营业日对商户支付、退款与拒付
扣回净额结算出款，按比例滚动留存保证金、到期释放，净额为负时动用保证金。

- 实现：`settlement/`（引擎、商户状态机、营业日日历、可区分错误）
- 独立朴素模型：`settlement/naive/`（随机差分测试参照）
- 设计说明（关键取舍、被放弃的方案、本地验证方法）：`docs/settlement-design.md`

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 随机对照日志（输入、输出与判定依据）
go test ./settlement/ -run TestDifferentialAgainstNaiveModel -v

# 单日结算开销与历史规模无关的基准证明
go test ./settlement/ -run '^$' -bench BenchmarkSettleSteadyState -benchtime 2000x -v
```

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
