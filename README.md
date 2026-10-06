# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子包：planningpoker

团队估点投票会话服务（隐藏投票 / 惰性到期 / 自动揭示 / 轮次上限 /
收敛与分歧判定 / 末轮强制取值）。

- 设计说明：`planningpoker/DESIGN.md`
- 包文档：`planningpoker/doc.go`
- 独立朴素对照模型与 1500 组随机差分测试：`planningpoker/oracle_test.go`、
  `planningpoker/diff_test.go`

```bash
go test -run TestDifferentialRandom -v ./planningpoker
go test -bench . -run '^$' ./planningpoker
```

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
