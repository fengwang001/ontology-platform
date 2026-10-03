# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多收件人投递回执

根包提供可并发调用的多收件人消息投递回执状态机，覆盖乱序、重复、迟到回执、软/硬退信、截止时刻到期与到期撤销。

- 规则与 API 说明：[DELIVERY_RECEIPTS.md](DELIVERY_RECEIPTS.md)
- 实现：`tracker.go`、`types.go`
- 定点规则、竞态测试与 2000 组朴素模型对拍：`*_test.go`

## 环境要求

- Go 1.26+（`go version` 确认）

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test -run TestRandomSequencesAgainstNaiveOracle -v ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
