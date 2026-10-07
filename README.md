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

## 权限决策审计回放模块

- 生产实现：`audit/`（版本哈希链、审计/纠正哈希链、无缓存回放、三态合法性查询、结构化调用日志）。
- 朴素参照：`naive/`（独立实现，供随机差分对照）。
- 设计说明（关键取舍、被放弃方案、证明方法）：`docs/DESIGN.md`。
- 关键测试：
  - `go test -run TestReplay ./audit/`：回放一致/不一致/篡改/缺失全组合。
  - `go test -run TestCorrection ./audit/`：多纠正覆盖关系与原始记录不可变。
  - `go test -run TestIntegrityCombinationBoundaries -v ./audit/`：完整性破坏组合边界。
  - `go test -run TestReplayReadsOnlyNamedVersion -v ./audit/`：回放只读登记版本的可观测证明。
  - `go test -run TestNaiveDifferential -v ./audit/`：与朴素实现的随机逐项对照。

最小用法见 `audit/example_test.go`。
