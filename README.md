# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 即时配送取消责任判定与退款分摊（`cancel/`）

- 实现：`cancel/types.go`、`cancel/errors.go`、`cancel/ledger.go`、`cancel/arbitration.go`、`cancel/system.go`
- 设计说明（关键取舍、放弃方案、验证方法）：`cancel/DESIGN.md`
- 测试：`go test ./...`；朴素模型差分：`go test -run TestNaiveDifferential -v`；竞态：`go test -race ./...`

包 `cancel` 以全局单调整数时钟、按阶段的取消裁决、争议窗口到期最小堆与
四方守恒账目，精确复现责任方与退款分摊。所有拒绝原因均为可程序化区分的
`ErrKind`，被拒操作不改变任何状态。

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
