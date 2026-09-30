# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 目录

- `merge/`：部分列更新事件的批内合并（详见 [docs/merge.md](docs/merge.md)）。
- `cmd/merge-demo/`：合并过程的可执行演示，打印每步输入、合并结果与判定依据。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 运行合并演示（打印逐步日志）
go run ./cmd/merge-demo

# 编译后运行
go build -o bin/merge-demo ./cmd/merge-demo
./bin/merge-demo
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./merge
go test -run TestConsistencyWithNaiveReference ./merge

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
