# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `snapshot/`：快照导出协同组件。在持续有新写入到达的情况下，产出对应
  明确逻辑边界（LSN）的全量快照与紧随其后的增量记录流，下游可将两者
  无缝拼接还原任意时点的完整自洽状态。设计取舍、错误分类与验证方法见
  [docs/snapshot-export-design.md](docs/snapshot-export-design.md)。
- `cmd/server/`：演示程序，展示持续写入下的快照导出与增量拼接。

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
