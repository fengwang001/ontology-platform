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

## 拒付案件与商户资金扣回

- 实现：`chargeback/`（入口 `chargeback.New(Config)`）
- 设计说明（取舍、被放弃方案、复杂度论证）：`chargeback/DESIGN.md`
- 独立朴素参考模型：`chargeback/naive/`
- 随机差分对照（打印输入/输出/判定依据）：
  `go test -run TestDifferentialRandom ./chargeback/`，加 `-diffverbose` 查看逐步日志

## 代码检查

```bash
gofmt -l .
go vet ./...
```
