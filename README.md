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

## 学籍异动状态机引擎

本仓库的 `enrollment` 包实现题目要求的学籍异动状态机引擎（与 cmd/server 无关）：

- 入口：`NewEngine`、`Admit`、`Submit`、`Decide`、`AddMajor`/`AddQuota`、
  `Graduate`、`SnapshotAt`、`AuditLog`。
- 设计取舍、被放弃方案与局部性证明见 [DESIGN.md](DESIGN.md)。
- 测试：规定场景在 `engine_test.go`（截止取等、累计上限取等、多层驳回、时限取等、
  名额不足后加名额、生效时刻早于最新版本、惰性退学、学期边界、拒绝优先级逐对验证、
  拒绝不审计、并发串行化）；独立朴素模型在 `naive_test.go`，60 种子×400 步随机
  对照（每步打印输入、输出与判定依据）在 `differential_test.go`。
