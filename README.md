# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 在途库存调拨系统

仓库间库存调拨（创建冻结 → 发出 → 分批收货 → 差异登记 → 关闭 → 事后找回）
的在途管理实现位于：

- 设计说明（模块划分、守恒模型、并发协议、被放弃方案）：[`DESIGN.md`](DESIGN.md)
- 使用文档与 API：[`README_TRANSFER.md`](README_TRANSFER.md)
- 实现：`internal/clock`、`internal/inventory`、`internal/transfer`
- 独立朴素参考模型：`internal/naive`（随机差分测试对照）

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
