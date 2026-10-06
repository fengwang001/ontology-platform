# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 住宅租约续签与租金涨幅管制

本仓库根包 `ontology` 实现了住宅租约到期续签与法定涨幅管制服务：
续签要约的发出/撤回、租户接受/拒绝/反要约、房东对反要约答复、
逾期与保护期触发的按月延续（租金冻结）、延续终止通知，以及按整数
金额判定的涨幅上限（基点档位 + 封顶）。

- 设计取舍与被放弃方案见 `DESIGN.md`。
- 模块：`config.go`、`errors.go`、`lease.go`、`scheduler.go`、
  `service.go`（堆实现）、`naive_all.go`（独立朴素模型）、`doc.go`。
- 测试：`service_all_test.go` 覆盖窗口/截止/涨幅/反要约/保护期等
  边界、错误次序、被拒不留痕、复杂度实测，以及朴素模型随机对照
  （日志逐步打印输入、输出与判定依据）。

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
