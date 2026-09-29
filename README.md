# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 当前模块提供 pitr（带时间线分叉的时间点恢复）包
ls pitr
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./pitr
go test -run TestThreeLevelPlansAndEquivalence ./pitr

# 竞态检测 + 重复执行（验证并发恢复编号连续、计划确定）
go test -race -count=5 ./pitr

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
