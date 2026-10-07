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

## 脱敏与可见性策略冲突裁决模块

策略仲裁实现在 `policy` 包，设计取舍、被放弃方案与验证方法见 `docs/DESIGN.md`。

```bash
# 端到端演示（两种主体呈现、原子策略变更、JSON 审计日志）
go run ./cmd/demo

# 含 4000 组随机策略集与朴素参照实现的差分对照
go test -run TestDifferentialAgainstNaive -v ./policy

# 策略考察开销不随登记总量增长的可观测证明
go test -run TestExaminationCostIndependentOfTotalPolicies -v ./policy
```
