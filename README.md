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

## PBFT 固定视图副本日志

`pbftlog/` 实现单个 PBFT 副本在固定视图内的请求日志：按
（视图、序号、摘要）收集预准备/准备/提交，按“已准备（Prepared，≥2f 个
非主节点 Prepare）”与“本地已提交（CommittedLocal，≥2f+1 个副本 Commit）”
两级证书判定，并按序号严格升序执行。两级证书条件、窗口随执行推进规则、
错误优先级、不建模范围（无视图变更/检查点）与本地验证方法见
[`pbftlog/README.md`](pbftlog/README.md)。
